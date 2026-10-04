package mirror_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/github"
	"github.com/rosenhouse/lg/internal/mirror"
	"github.com/rosenhouse/lg/internal/model"
)

var _ = Describe("NewestFirst", Label("artifacts"), func() {
	It("orders runs by created_at, newest first, whatever their listing order", func() {
		at := func(hour int) github.Run {
			return github.Run{Run: model.Run{CreatedAt: time.Date(2026, 10, 3, hour, 0, 0, 0, time.UTC)}}
		}

		Expect(mirror.NewestFirst([]github.Run{at(14), at(16), at(15)})).To(Equal([]int{1, 2, 0}))
	})
})
