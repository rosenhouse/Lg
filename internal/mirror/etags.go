package mirror

import (
	"bytes"
	"encoding/json"
	"maps"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/store"
)

const etagsFile = "etags.json"

// etags is state/etags.json: by URL, the answers that the next cycle's GETs
// revalidate.
type etags struct {
	store   *store.Store
	earlier map[string]github.Answer
	cache   *github.Cache
}

func loadETags(s *store.Store) (e *etags, discarded, err error) {
	var earlier map[string]github.Answer
	discarded, err = s.ReadState(etagsFile, func(raw []byte) error {
		var decoded map[string]github.Answer
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		earlier = decoded
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return &etags{store: s, earlier: earlier, cache: github.NewCache(earlier)}, discarded, nil
}

// save writes the answers the cycle asked for, so that URLs no cycle GETs
// drop out. A cycle that stopped early keeps every answer. It writes the
// file only when the answers changed.
func (e *etags) save(completed bool) error {
	answers := e.cache.All()
	if completed {
		answers = e.cache.Asked()
	}
	if maps.EqualFunc(answers, e.earlier, sameAnswer) {
		return nil
	}
	return e.store.WriteState(etagsFile, answers)
}

func sameAnswer(a, b github.Answer) bool {
	return a.ETag == b.ETag && a.Link == b.Link && bytes.Equal(a.Body, b.Body)
}
