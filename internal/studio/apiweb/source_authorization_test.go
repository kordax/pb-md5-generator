package apiweb

import (
	"context"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

func authorizeSourceForHTTPTest(ctx context.Context, actor content.Actor, name string, access content.SourceAccess) error {
	user, err := Principal(ctx)
	if err != nil {
		return auth.ErrUnauthorized
	}
	if user == nil || actor.ID == "" || actor.ID != user.ID {
		return auth.ErrForbidden
	}
	if !permitted(user, auth.ActionRead, name) {
		return content.ErrNotFound
	}
	if access != content.SourceRead && access != content.SourceReadWrite || access == content.SourceReadWrite && !permitted(user, auth.ActionWrite, name) {
		return auth.ErrForbidden
	}
	return ctx.Err()
}
