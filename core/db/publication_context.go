package db

import "context"

type publicationKey struct{}
type publicationContext struct {
	actor    string
	position *Position
}

// WithPublication captures the exact committed position for adapters whose
// established API returns an entity rather than a transaction result.
func WithPublication(ctx context.Context, actor string) (context.Context, *Position) {
	p := &Position{}
	return context.WithValue(ctx, publicationKey{}, publicationContext{actor: actor, position: p}), p
}

func publicationActor(ctx context.Context) string {
	v, _ := ctx.Value(publicationKey{}).(publicationContext)
	return v.actor
}
func notePublication(ctx context.Context, p Position) {
	if v, ok := ctx.Value(publicationKey{}).(publicationContext); ok {
		*v.position = p
	}
}
