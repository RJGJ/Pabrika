package service

import "context"

// labelService is a stub; the labels work package replaces this file.
type labelService struct{ s *Services }

func (l *labelService) List(ctx context.Context, actor Actor, projectRef string) ([]Label, error) {
	return nil, errNotImplemented
}
func (l *labelService) Create(ctx context.Context, actor Actor, projectRef string, in LabelInput) (Label, error) {
	return Label{}, errNotImplemented
}
func (l *labelService) Update(ctx context.Context, actor Actor, labelID string, in UpdateLabelInput) (Label, error) {
	return Label{}, errNotImplemented
}
func (l *labelService) Delete(ctx context.Context, actor Actor, labelID string) error {
	return errNotImplemented
}
