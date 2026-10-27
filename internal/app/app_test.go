package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/galileostudio/baleia/internal/config"
)

func TestNew_RequiresService(t *testing.T) {
	t.Parallel()
	if _, err := New(context.Background(), &config.Config{}, Options{}); !errors.Is(err, ErrNoService) {
		t.Fatalf("err = %v, want ErrNoService", err)
	}
}

// TestClose_ReverseOrderOnce: the last built is released first, and a second
// Close does nothing.
func TestClose_ReverseOrderOnce(t *testing.T) {
	t.Parallel()
	var got []int
	c := &Core{}
	for i := range 3 {
		c.onClose(func() { got = append(got, i) })
	}
	c.Close()
	c.Close()
	if !slices.Equal(got, []int{2, 1, 0}) {
		t.Fatalf("close order = %v, want [2 1 0]", got)
	}
}
