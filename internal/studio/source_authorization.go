package studio

import (
	"context"

	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

// authorizeSource refreshes the session inside the content gate, after any
// queued work, without reentering the content service.
func authorizeSource(ctx context.Context, actor content.Actor, name string, access content.SourceAccess) error {
	user, err := apiweb.Principal(ctx)
	if err != nil {
		return auth.ErrUnauthorized
	}
	if user == nil || actor.ID == "" || user.ID != actor.ID {
		return auth.ErrForbidden
	}
	if access != content.SourceRead && access != content.SourceReadWrite {
		return auth.ErrForbidden
	}
	if !canAccessSource(user, auth.ActionRead, name) || access == content.SourceReadWrite && !canAccessSource(user, auth.ActionWrite, name) {
		return auth.ErrForbidden
	}
	return ctx.Err()
}
