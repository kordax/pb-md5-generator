package apiweb

import (
	"net/http"

	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

func (h *handler) currentDrafts(r *http.Request, drafts []content.Draft) ([]content.Draft, error) {
	status, err := h.opts.Content.Status(r.Context())
	if err != nil {
		return nil, err
	}
	matches := map[string]bool{status.Branch: true}
	items := make([]content.Draft, 0, len(drafts))
	for _, draft := range drafts {
		current, found := matches[draft.Branch]
		if !found {
			var err error
			current, err = h.opts.Content.MatchesCurrentBranch(r.Context(), draft.Branch)
			if err != nil {
				return nil, err
			}
			matches[draft.Branch] = current
		}
		if current {
			items = append(items, draft)
		}
	}
	return items, nil
}
