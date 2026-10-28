package appearance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/httpjson"
)

type navigationInput struct {
	ShowTOC          bool            `json:"showTOC"`
	ShowPreviousNext bool            `json:"showPreviousNext"`
	StickyHeader     bool            `json:"stickyHeader"`
	Tree             json.RawMessage `json:"tree,omitempty"`
}

type settingsInput struct {
	Revision    string                      `json:"revision"`
	Title       string                      `json:"title"`
	Description string                      `json:"description"`
	Logo        string                      `json:"logo"`
	Favicon     string                      `json:"favicon"`
	Footer      string                      `json:"footer"`
	ThemeID     string                      `json:"themeId"`
	Theme       document.Theme              `json:"theme"`
	Navigation  navigationInput             `json:"navigation"`
	Editor      document.EditorSettings     `json:"editor"`
	API         document.APISettings        `json:"api"`
	Publishing  document.PublishingSettings `json:"publishing"`
	Sync        *content.SyncStatus         `json:"sync,omitempty"`
}

func (input settingsInput) settings() (Settings, error) {
	settings := Settings{Revision: input.Revision, Title: input.Title, Description: input.Description, Logo: input.Logo, Favicon: input.Favicon, Footer: input.Footer, ThemeID: input.ThemeID, Theme: input.Theme, Navigation: document.NavigationSettings{ShowTOC: input.Navigation.ShowTOC, ShowPreviousNext: input.Navigation.ShowPreviousNext, StickyHeader: input.Navigation.StickyHeader}, Editor: input.Editor, API: input.API, Publishing: input.Publishing}
	settings.NavigationTreeSet = len(input.Navigation.Tree) != 0
	if settings.NavigationTreeSet && !bytes.Equal(bytes.TrimSpace(input.Navigation.Tree), []byte("null")) {
		var tree document.NavigationTree
		if err := httpjson.Unmarshal(input.Navigation.Tree, &tree); err != nil {
			return Settings{}, err
		}
		settings.Navigation.Tree = &tree
	}
	return settings, nil
}

func (s *Service) navigationAllowed(ctx context.Context, user *auth.User) (bool, error) {
	if !auth.Can(user, auth.ActionAdmin, "") || !auth.Can(user, auth.ActionRead, ".") || !auth.Can(user, auth.ActionWrite, ".") {
		return false, nil
	}
	if s.authorizeNavigation != nil {
		if err := s.authorizeNavigation(ctx, actor(user)); err != nil {
			if errors.Is(err, auth.ErrForbidden) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}

func (s *Service) settingsResponse(writer http.ResponseWriter, request *http.Request, settings Settings, authentication *auth.Service) {
	user, err := authentication.Resolve(request)
	if err != nil || user == nil {
		serviceFailure(writer, auth.ErrUnauthorized)
		return
	}
	if settings.Navigation.Tree != nil {
		allowed, err := s.navigationAllowed(request.Context(), user)
		if err != nil {
			serviceFailure(writer, err)
			return
		}
		if !allowed {
			settings.Navigation.Tree = nil
		}
	}
	writer.Header().Set("ETag", `"`+settings.Revision+`"`)
	writeJSON(writer, http.StatusOK, settings)
}
