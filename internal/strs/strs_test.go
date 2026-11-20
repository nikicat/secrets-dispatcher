package strs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type name string

func TestOf(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, Of([]name{"a", "b"}))
	assert.Empty(t, Of[name](nil))
}
