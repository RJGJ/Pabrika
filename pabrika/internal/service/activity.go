package service

import "context"

// activityService is a stub; the activity work package replaces this file.
type activityService struct{ s *Services }

func (a *activityService) List(ctx context.Context, actor Actor, ticketRef string, limit int, cursor string) (ActivityPage, error) {
	return ActivityPage{}, errNotImplemented
}
