package mirror_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rosenhouse/lg/internal/mirror"
)

func TestMirror(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mirror Suite")
}

func cycleErr(ctx context.Context, m *mirror.Mirror) error {
	_, err := m.Cycle(ctx)
	return err
}
