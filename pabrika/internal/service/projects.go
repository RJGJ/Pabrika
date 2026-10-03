package service

import "context"

// projectService is a stub; the projects work package replaces this file.
type projectService struct{ s *Services }

func (p *projectService) Create(ctx context.Context, actor Actor, in CreateProjectInput) (Project, error) {
	return Project{}, errNotImplemented
}
func (p *projectService) Get(ctx context.Context, actor Actor, ref string) (ProjectDetail, error) {
	return ProjectDetail{}, errNotImplemented
}
func (p *projectService) Resolve(ctx context.Context, actor Actor, ref string) (ProjectRef, error) {
	return ProjectRef{}, errNotImplemented
}
func (p *projectService) List(ctx context.Context, actor Actor, includeArchived bool) ([]ProjectSummary, error) {
	return nil, errNotImplemented
}
func (p *projectService) Update(ctx context.Context, actor Actor, ref string, in UpdateProjectInput) (Project, error) {
	return Project{}, errNotImplemented
}
func (p *projectService) Delete(ctx context.Context, actor Actor, ref string) error {
	return errNotImplemented
}
