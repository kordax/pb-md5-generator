package studio

import (
	"context"

	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

// authorizeStorage runs inside the content gate so a queued operation cannot
// retain permissions from a session that was changed while it waited.
func authorizeStorage(ctx context.Context, actor content.Actor) error {
	user, err := apiweb.Principal(ctx)
	if err != nil {
		return auth.ErrUnauthorized
	}
	if user == nil || !auth.Can(user, auth.ActionAdmin, ".") || !auth.Can(user, auth.ActionRead, ".") || !auth.Can(user, auth.ActionWrite, ".") {
		return auth.ErrForbidden
	}
	if actor.ID != "" && actor.ID != user.ID {
		return auth.ErrForbidden
	}
	return ctx.Err()
}
