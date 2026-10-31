package usersservice

import (
	"context"
	"errors"
	"testing"

	"github.com/moduleforge/core-api/display"
	coredb "github.com/moduleforge/core-model/db"
)

// slugQuerier resolves every entity id to a fixed fundamental type slug.
type slugQuerier struct {
	coredb.Querier
	slug string
}

func (q slugQuerier) GetEntityByID(context.Context, int64) (coredb.GetEntityByIDRow, error) {
	return coredb.GetEntityByIDRow{FundamentalTypeSlug: q.slug}, nil
}

// renders reports whether reg has a renderer for the slug's description field.
// The description renderer reads nothing, so it needs no database.
func renders(t *testing.T, slug string, wire func(*display.Registry)) bool {
	t.Helper()
	reg := display.NewRegistry(slugQuerier{slug: slug})
	wire(reg)
	_, err := reg.Render(context.Background(), nil, 1, display.FieldDescription)
	if err != nil && !errors.Is(err, display.ErrRendererNotRegistered) {
		t.Fatalf("render %q: unexpected error: %v", slug, err)
	}
	return err == nil
}

func TestNewUserServiceWithSelfServiceRegistersDisplayRenderers(t *testing.T) {
	construct := func(reg *display.Registry) {
		NewUserServiceWithSelfService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, 0, reg)
	}
	for _, slug := range []string{"user", "profile"} {
		if !renders(t, slug, construct) {
			t.Errorf("no display renderer registered for %q", slug)
		}
	}
	if renders(t, "other", construct) {
		t.Error("renderer registered for an unrelated type")
	}
}

func TestNewUserServiceWithSelfServiceNilRegistry(t *testing.T) {
	if NewUserServiceWithSelfService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, 0, nil) == nil {
		t.Fatal("nil service")
	}
}
