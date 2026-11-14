package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/entity"
)

func TestMaskPhone(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "[invalid]", entity.MaskPhone(""))
	assert.Equal(t, "***", entity.MaskPhone("123456"))
	assert.Equal(t, "138****8000", entity.MaskPhone("13800138000"))
}
