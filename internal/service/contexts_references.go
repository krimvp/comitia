package service

import (
	"context"
	"net/url"

	"github.com/krimvp/comitia/internal/model"
)

// ListReferences selects reference listing without changing ContextAPI.
func ListReferences(ctx context.Context, api model.ContextAPI, token, cid, after string, limit int) (model.ReferencePage, error) {
	pages, ok := api.(model.ReferenceListAPI)
	if !ok {
		return model.ReferencePage{}, model.Invalidf("reference listing unsupported by this API")
	}
	return pages.ListReferences(ctx, token, cid, after, limit)
}

// ListReferences needs read on the context, including inherited parent read;
// archived contexts stay readable. Pages stop early at ListPageBytes.
func (s *Service) ListReferences(ctx context.Context, token, cid, after string, limit int) (model.ReferencePage, error) {
	if limit < 1 || limit > 1000 {
		return model.ReferencePage{}, model.Invalidf("limit must be between 1 and 1000")
	}
	cursor, err := decodeCreatedCursor(after, "reference")
	if err != nil {
		return model.ReferencePage{}, err
	}
	page := model.ReferencePage{References: []model.Reference{}, NextAfter: after}
	err = s.store.View(ctx, func(r model.ReadStore) error {
		if _, _, err := s.require(r, token, cid, model.PermissionRead); err != nil {
			return err
		}
		pages, ok := r.(model.ReferenceListStore)
		if !ok {
			return model.Invalidf("reference listing unsupported by this store")
		}
		refs, err := pages.ReferencesAfter(cid, cursor.CreatedAt, cursor.ID, limit+1)
		if err != nil {
			return err
		}
		page.HasMore = len(refs) > limit
		if page.HasMore {
			refs = refs[:limit]
		}
		if len(refs) > 0 {
			page.References = refs
		}
		return nil
	})
	if err != nil {
		return model.ReferencePage{}, err
	}
	n, err := boundedCount(page.References, model.ListPageBytes)
	if err != nil {
		return model.ReferencePage{}, err
	}
	if n < len(page.References) {
		page.References, page.HasMore = page.References[:n], true
	}
	if n > 0 {
		last := page.References[n-1]
		page.NextAfter = encodeCreatedCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

// validReference bounds new references; stored rows stay readable.
func validReference(in model.AddReferenceInput) error {
	if in.Kind == "" || in.URI == "" {
		return model.Invalidf("kind and uri required")
	}
	if e := model.ValidText("kind", in.Kind, model.MaxReferenceKindBytes); e != nil {
		return e
	}
	if e := model.ValidText("uri", in.URI, model.MaxReferenceURIBytes); e != nil {
		return e
	}
	for i, c := range in.Kind {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '.' || c == '_' || c == '-' || c == '+' || c == '/')) {
			return model.Invalidf("kind must start with a letter or digit and contain only letters, digits, '.', '_', '-', '+' or '/'")
		}
	}
	if u, err := url.Parse(in.URI); err != nil || u.Scheme == "" {
		return model.Invalidf("uri must be an absolute URI with a scheme")
	}
	return nil
}
