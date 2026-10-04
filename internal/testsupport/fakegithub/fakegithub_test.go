package fakegithub_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/recordings"
)

const (
	logsDeletedRun = 37129738159
	repoID         = "1402714635"
)

type response struct {
	status int
	header http.Header
	body   []byte
}

// fetch sends one GET and does not follow redirects.
func fetch(rawURL string, header ...string) response {
	GinkgoHelper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, http.NoBody)
	Expect(err).NotTo(HaveOccurred())
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return response{status: resp.StatusCode, header: resp.Header, body: body}
}

// recordedFile maps a status.txt path to the file record.sh wrote it to.
func recordedFile(dir, path string) string {
	GinkgoHelper()
	routes := []struct {
		pattern *regexp.Regexp
		file    string
	}{
		{regexp.MustCompile(`^runs/\d+$`), "run.json"},
		{regexp.MustCompile(`^runs/\d+/jobs\?filter=all&`), "jobs-all.json"},
		{regexp.MustCompile(`^runs/\d+/jobs\?filter=latest&`), "jobs-latest.json"},
		{regexp.MustCompile(`^runs/\d+/artifacts\?`), "artifacts.json"},
		{regexp.MustCompile(`^runs/\d+/attempts/(\d+)$`), "attempt-$1/attempt.json"},
		{regexp.MustCompile(`^runs/\d+/attempts/(\d+)/jobs\?`), "attempt-$1/jobs.json"},
		{regexp.MustCompile(`^runs/\d+/attempts/(\d+)/logs$`), "attempt-$1/logs.zip"},
		{regexp.MustCompile(`^jobs/(\d+)/logs$`), "attempt-*/logs/$1.txt"},
		{regexp.MustCompile(`^artifacts/(\d+)/zip$`), "artifacts/$1.zip"},
	}
	for _, r := range routes {
		if m := r.pattern.FindStringSubmatchIndex(path); m != nil {
			glob := string(r.pattern.ExpandString(nil, r.file, path, m))
			files, err := filepath.Glob(filepath.Join(dir, glob))
			Expect(err).NotTo(HaveOccurred())
			Expect(files).To(HaveLen(1), path)
			return files[0]
		}
	}
	Fail("no recorded file for " + path)
	return ""
}

// listing GETs a listing and every page its Link next URLs lead to,
// returning the elements of field and the URLs followed.
func listing(rawURL, field string) (elements []json.RawMessage, followed []string) {
	GinkgoHelper()
	for rawURL != "" {
		resp := fetch(rawURL)
		Expect(resp.status).To(Equal(http.StatusOK), rawURL)
		var page map[string]json.RawMessage
		Expect(json.Unmarshal(resp.body, &page)).To(Succeed())
		var items []json.RawMessage
		Expect(json.Unmarshal(page[field], &items)).To(Succeed())
		elements = append(elements, items...)
		rawURL = nextLink(resp.header.Get("Link"))
		if rawURL != "" {
			followed = append(followed, rawURL)
		}
	}
	return elements, followed
}

var linkNext = regexp.MustCompile(`<([^>]+)>; rel="next"`)

func nextLink(header string) string {
	if m := linkNext.FindStringSubmatch(header); m != nil {
		return m[1]
	}
	return ""
}

func recordedStatus(dir string) []recordings.Line {
	GinkgoHelper()
	status, err := os.Open(filepath.Join(dir, "status.txt"))
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = status.Close() }()
	lines, err := recordings.ParseStatus(status)
	Expect(err).NotTo(HaveOccurred())
	return lines
}

func recordedElements(file, field string) []json.RawMessage {
	GinkgoHelper()
	raw, err := os.ReadFile(file)
	Expect(err).NotTo(HaveOccurred())
	var recorded map[string]json.RawMessage
	Expect(json.Unmarshal(raw, &recorded)).To(Succeed())
	var items []json.RawMessage
	Expect(json.Unmarshal(recorded[field], &items)).To(Succeed())
	return items
}

func expectJSONElements(actual, expected []json.RawMessage) {
	GinkgoHelper()
	Expect(actual).To(HaveLen(len(expected)))
	for i := range expected {
		Expect(actual[i]).To(MatchJSON(expected[i]), "element %d", i)
	}
}

var _ = Describe("fakegithub replay", Label("transport"), func() {
	It("serves every line of every recorded status.txt with its first-hop and final status; JSON comes back compact and JSON-equal to the recording; logs, zips and BlobNotFound XML come back byte-identical", func() {
		for _, r := range []struct {
			run   int64
			stage string
		}{
			{runID, "after-attempt-1"},
			{runID, "after-attempt-2"},
			{runID, "after-attempt-3"},
			{logsDeletedRun, "logs-deleted"},
		} {
			dir := recordings.Dir(r.run, r.stage)
			fake := fakegithub.Start(r.run, r.stage)

			lines := recordedStatus(dir)
			Expect(len(lines)).To(BeNumerically(">", 10))
			for _, line := range lines {
				resp := fetch(fake.URL() + "/repos/rosenhouse/lg/actions/" + line.Path)
				Expect(resp.status).To(Equal(line.First), line.Path)
				if line.First != line.Final {
					resp = fetch(resp.header.Get("Location"))
					Expect(resp.status).To(Equal(line.Final), line.Path)
				}

				recorded, err := os.ReadFile(recordedFile(dir, line.Path))
				Expect(err).NotTo(HaveOccurred())
				if json.Valid(recorded) {
					var compact bytes.Buffer
					Expect(json.Compact(&compact, resp.body)).To(Succeed(), line.Path)
					Expect(resp.body).To(Equal(compact.Bytes()), line.Path)
					Expect(resp.body).To(MatchJSON(recorded), line.Path)
				} else {
					Expect(resp.body).To(Equal(recorded), line.Path)
				}
			}
		}
	})
})

var _ = Describe("fakegithub", Label("transport"), func() {
	It("serves blobs from a host on another domain that answers 401 to Authorization: Bearer and 403 to any other Authorization", func() {
		fake := fakegithub.Start(runID, "after-attempt-1")
		redirect := fetch(fake.URL() + logPath)
		Expect(redirect.status).To(Equal(http.StatusFound))
		blob, err := url.Parse(redirect.header.Get("Location"))
		Expect(err).NotTo(HaveOccurred())
		api, err := url.Parse(fake.URL())
		Expect(err).NotTo(HaveOccurred())
		Expect(blob.Hostname()).To(Equal("localhost"))
		Expect(api.Hostname()).To(Equal("127.0.0.1"))

		Expect(fetch(blob.String()).status).To(Equal(http.StatusOK))
		bearer := fetch(blob.String(), "Authorization", "Bearer lg-test-token")
		Expect(bearer.status).To(Equal(http.StatusUnauthorized))
		Expect(bearer.header.Get("WWW-Authenticate")).To(HavePrefix("Bearer "))
		Expect(string(bearer.body)).To(HavePrefix("\uFEFF<?xml"))
		Expect(string(bearer.body)).To(ContainSubstring("<Code>InvalidAuthenticationInfo</Code>"))
		for _, scheme := range []string{"token", "Basic"} {
			other := fetch(blob.String(), "Authorization", scheme+" lg-test-token")
			Expect(other.status).To(Equal(http.StatusForbidden), scheme)
			Expect(string(other.body)).To(HavePrefix("\uFEFF<?xml"))
			Expect(string(other.body)).To(ContainSubstring("<Code>AuthenticationFailed</Code>"), scheme)
		}
	})

	It("pages run, job and artifact listings with Link rel=next URLs in /repositories/1402714635/ form when a page cap is set, and serves those URLs", func() {
		fake := fakegithub.Start(runID, "after-attempt-1")
		Expect(fake.Load(logsDeletedRun, "logs-deleted")).To(Succeed())
		fake.SetPageCap(1)
		api, err := url.Parse(fake.URL())
		Expect(err).NotTo(HaveOccurred())
		actions := fake.URL() + "/repos/rosenhouse/lg/actions/"
		recording := recordings.Dir(runID, "after-attempt-1")

		for _, l := range []struct {
			path, field string
			want        []json.RawMessage
		}{
			{"runs?per_page=100", "workflow_runs", []json.RawMessage{
				recordedRun(recordings.Dir(logsDeletedRun, "logs-deleted")),
				recordedRun(recording),
			}},
			{"runs/37129390741/jobs?filter=all&per_page=100", "jobs", recordedElements(filepath.Join(recording, "jobs-all.json"), "jobs")},
			{"runs/37129390741/attempts/1/jobs?per_page=100", "jobs", recordedElements(filepath.Join(recording, "attempt-1", "jobs.json"), "jobs")},
			{"runs/37129390741/artifacts?per_page=100", "artifacts", recordedElements(filepath.Join(recording, "artifacts.json"), "artifacts")},
		} {
			elements, followed := listing(actions+l.path, l.field)
			expectJSONElements(elements, l.want)
			Expect(followed).To(HaveLen(len(l.want)-1), l.path)
			for _, next := range followed {
				u, err := url.Parse(next)
				Expect(err).NotTo(HaveOccurred())
				Expect(u.Host).To(Equal(api.Host))
				Expect(u.Path).To(HavePrefix("/repositories/" + repoID + "/actions/"))
			}
		}
	})

	It("serves every route under /api/v3 as well", func() {
		fake := fakegithub.Start(runID, "after-attempt-1")
		recording := recordings.Dir(runID, "after-attempt-1")

		routes := map[string]int{
			"/repos/rosenhouse/lg":                               http.StatusOK,
			"/repos/rosenhouse/lg/actions/runs?per_page=100":     http.StatusOK,
			"/repos/rosenhouse/lg/actions/artifacts/11276401837": http.StatusOK,
		}
		for _, line := range recordedStatus(recording) {
			routes["/repos/rosenhouse/lg/actions/"+line.Path] = line.First
		}
		for path, code := range routes {
			root := fetch(fake.URL() + path)
			v3 := fetch(fake.URL() + "/api/v3" + path)
			Expect(v3.status).To(Equal(code), path)
			Expect(v3.body).To(Equal(root.body), path)
			Expect(v3.header.Get("Location")).To(Equal(root.header.Get("Location")), path)
		}

		fake.SetPageCap(5)
		elements, followed := listing(fake.URL()+"/api/v3/repos/rosenhouse/lg/actions/runs/37129390741/attempts/1/jobs?per_page=100", "jobs")
		expectJSONElements(elements, recordedElements(filepath.Join(recording, "attempt-1", "jobs.json"), "jobs"))
		Expect(followed).To(HaveLen(2))
		for _, next := range followed {
			Expect(next).To(HavePrefix(fake.URL() + "/api/v3/repositories/" + repoID + "/actions/"))
		}
	})

	It("returns 404 for both the metadata and the zip of artifacts absent after Advance to after-attempt-3", func() {
		fake := fakegithub.Start(runID, "after-attempt-2")
		artifact := func(id json.RawMessage) string {
			return fake.URL() + "/repos/rosenhouse/lg/actions/artifacts/" + string(id)
		}
		ids := func(stage string) map[string]json.RawMessage {
			present := map[string]json.RawMessage{}
			for _, a := range recordedElements(filepath.Join(recordings.Dir(runID, stage), "artifacts.json"), "artifacts") {
				var meta struct{ ID json.RawMessage }
				Expect(json.Unmarshal(a, &meta)).To(Succeed())
				present[string(meta.ID)] = a
			}
			return present
		}
		before, after := ids("after-attempt-2"), ids("after-attempt-3")
		for id := range before {
			Expect(fetch(artifact(json.RawMessage(id))).status).To(Equal(http.StatusOK))
		}

		Expect(fake.Advance(runID, "after-attempt-3")).To(Succeed())

		for id := range before {
			Expect(after).NotTo(HaveKey(id))
			Expect(fetch(artifact(json.RawMessage(id))).status).To(Equal(http.StatusNotFound), id)
			Expect(fetch(artifact(json.RawMessage(id))+"/zip").status).To(Equal(http.StatusNotFound), id)
		}
		for id, meta := range after {
			resp := fetch(artifact(json.RawMessage(id)))
			Expect(resp.status).To(Equal(http.StatusOK), id)
			Expect(resp.body).To(MatchJSON(meta))
			Expect(fetch(artifact(json.RawMessage(id))+"/zip").status).To(Equal(http.StatusFound), id)
		}
	})

	It("serves GET /repos/rosenhouse/lg case-insensitively with full_name rosenhouse/Lg and default_branch main", func() {
		fake := fakegithub.Start(runID, "after-attempt-1")
		for _, path := range []string{"/repos/rosenhouse/lg", "/repos/Rosenhouse/LG", "/repositories/" + repoID} {
			resp := fetch(fake.URL() + path)
			Expect(resp.status).To(Equal(http.StatusOK), path)
			var repo map[string]any
			Expect(json.Unmarshal(resp.body, &repo)).To(Succeed())
			Expect(repo).To(HaveKeyWithValue("full_name", "rosenhouse/Lg"))
			Expect(repo).To(HaveKeyWithValue("default_branch", "main"))
		}
		Expect(fetch(fake.URL() + "/repos/rosenhouse/other").status).To(Equal(http.StatusNotFound))
	})

	DescribeTable("pages listings at per_page up to 100, or 30 when per_page is missing or below 1",
		func(query string, size int) {
			fake := fakegithub.New()
			DeferCleanup(fake.Close)
			for i := range 150 {
				fake.AddListed(json.RawMessage(fmt.Sprintf(`{"id":%d,"created_at":"2026-10-02T00:00:00Z","status":"completed"}`, i+1)))
			}

			resp := fetch(fake.URL() + "/repos/rosenhouse/lg/actions/runs" + query)
			Expect(resp.status).To(Equal(http.StatusOK))
			var page struct {
				WorkflowRuns []any `json:"workflow_runs"`
			}
			Expect(json.Unmarshal(resp.body, &page)).To(Succeed())
			Expect(page.WorkflowRuns).To(HaveLen(size))
		},
		Entry("per_page=7", "?per_page=7", 7),
		Entry("per_page=500", "?per_page=500", 100),
		Entry("no per_page", "", 30),
		Entry("per_page=0", "?per_page=0", 30),
	)

	It("returns at most 1,000 runs for a listing filtered by created or status", func() {
		fake := fakegithub.New()
		DeferCleanup(fake.Close)
		const added = 1050
		for i := range added {
			fake.AddListed(json.RawMessage(fmt.Sprintf(
				`{"id":%d,"created_at":"2026-10-02T%02d:%02d:00Z","status":"completed","conclusion":"success","repository":{"full_name":"rosenhouse/Lg"}}`,
				i+1, i/60, i%60)))
		}
		const unmatched = 5
		for i := range unmatched {
			fake.AddListed(json.RawMessage(fmt.Sprintf(
				`{"id":%d,"created_at":"2026-10-03T00:%02d:00Z","status":"in_progress","repository":{"full_name":"rosenhouse/Lg"}}`,
				added+i+1, i)))
		}
		runs := fake.URL() + "/repos/rosenhouse/lg/actions/runs?per_page=100"

		all, _ := listing(runs, "workflow_runs")
		Expect(all).To(HaveLen(unmatched + added))
		for _, filter := range []string{"created=2026-10-02", "status=completed", "status=success"} {
			filtered, _ := listing(runs+"&"+filter, "workflow_runs")
			Expect(filtered).To(Equal(all[unmatched:unmatched+1000]), filter)
		}
	})
})

func recordedRun(recording string) json.RawMessage {
	GinkgoHelper()
	raw, err := os.ReadFile(filepath.Join(recording, "run.json"))
	Expect(err).NotTo(HaveOccurred())
	return raw
}
