package e2e_test

import (
	"database/sql"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	_ "modernc.org/sqlite"

	"github.com/rosenhouse/lg/internal/testsupport/fakegithub"
	"github.com/rosenhouse/lg/internal/testsupport/harness"
)

var _ = Describe("lg index rebuild", Label("index"), func() {
	It("rebuilds while another process holds the db open", func(ctx SpecContext) {
		env := harness.New(lgPath)
		env.WriteConfig(fakegithub.Start(fixtureRun, "after-attempt-1").URL())
		Expect(env.Sync()).To(gexec.Exit(0))
		Eventually(env.Lg("index", "rebuild"), harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(filepath.Join(env.State(), "lg.db")).To(BeARegularFile())

		db, err := sql.Open("sqlite", filepath.Join(env.State(), "lg.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		jobs := func() (n int) {
			Expect(db.QueryRow("SELECT count(*) FROM jobs").Scan(&n)).To(Succeed())
			return n
		}
		_, err = db.Exec("DELETE FROM jobs")
		Expect(err).NotTo(HaveOccurred())
		reader, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = reader.Rollback() })
		var held int
		Expect(reader.QueryRow("SELECT count(*) FROM runs").Scan(&held)).To(Succeed())

		Eventually(env.Lg("index", "rebuild"), harness.ExitTimeout).Should(gexec.Exit(0))
		Expect(reader.Rollback()).To(Succeed())
		Expect(jobs()).To(Equal(12))
	}, SpecTimeout(30*time.Second))
})
