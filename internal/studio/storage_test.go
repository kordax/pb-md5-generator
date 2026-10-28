package studio

import (
	"context"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
)

func TestStorageAuthorizationRejectsInternalSystemActor(t *testing.T) {
	// Trusted compilation can use a system author; managing storage still
	// requires a freshly authenticated administrator with root access.
	assert.ErrorIs(t, authorizeStorage(context.Background(), content.Actor{ID: "system"}), auth.ErrForbidden)
}
