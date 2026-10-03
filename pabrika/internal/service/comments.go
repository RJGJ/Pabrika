package service

import "context"

// commentService is a stub; the comments work package replaces this file.
type commentService struct{ s *Services }

func (c *commentService) List(ctx context.Context, actor Actor, ticketRef string, limit int, cursor string) (CommentPage, error) {
	return CommentPage{}, errNotImplemented
}
func (c *commentService) Latest(ctx context.Context, actor Actor, ticketRef string, n int) ([]Comment, bool, error) {
	return nil, false, errNotImplemented
}
func (c *commentService) Add(ctx context.Context, actor Actor, ticketRef, body string) (Comment, error) {
	return Comment{}, errNotImplemented
}
func (c *commentService) Edit(ctx context.Context, actor Actor, commentID, body string) (Comment, error) {
	return Comment{}, errNotImplemented
}
func (c *commentService) Delete(ctx context.Context, actor Actor, commentID string) error {
	return errNotImplemented
}
func (c *commentService) Resolve(ctx context.Context, actor Actor, commentID string) (CommentRef, error) {
	return CommentRef{}, errNotImplemented
}
