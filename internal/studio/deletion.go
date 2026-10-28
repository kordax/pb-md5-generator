package studio

import (
	"context"

	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

// authorizeDeleteTree runs under the content gate before a deletion plan can
// disclose its inventory, and again before staging or publishing that plan.
func authorizeDeleteTree(ctx context.Context, actor content.Actor, entries []content.Entry) error {
	user, err := apiweb.Principal(ctx)
	if err != nil {
		return auth.ErrUnauthorized
	}
	if user == nil || actor.ID == "" || actor.ID != user.ID || len(entries) == 0 {
		return auth.ErrForbidden
	}
	for _, entry := range entries {
		if !canAccessSource(user, auth.ActionRead, entry.Path) || !canAccessSource(user, auth.ActionWrite, entry.Path) {
			return auth.ErrForbidden
		}
	}
	return ctx.Err()
}
