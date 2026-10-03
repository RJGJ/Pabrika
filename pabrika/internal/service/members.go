package service

import "context"

// memberService is a stub; the members work package replaces this file.
type memberService struct{ s *Services }

func (m *memberService) List(ctx context.Context, actor Actor, projectRef string) ([]Member, error) {
	return nil, errNotImplemented
}
func (m *memberService) Add(ctx context.Context, actor Actor, projectRef, email string, role Role) (Member, error) {
	return Member{}, errNotImplemented
}
func (m *memberService) SetRole(ctx context.Context, actor Actor, projectRef, userID string, role Role) (Member, error) {
	return Member{}, errNotImplemented
}
func (m *memberService) Remove(ctx context.Context, actor Actor, projectRef, userID string) error {
	return errNotImplemented
}
