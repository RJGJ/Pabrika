package service

import "context"

// ticketService is a stub; the tickets work package replaces this file.
type ticketService struct{ s *Services }

func (t *ticketService) Create(ctx context.Context, actor Actor, projectRef string, in CreateTicketInput) (Ticket, error) {
	return Ticket{}, errNotImplemented
}
func (t *ticketService) Get(ctx context.Context, actor Actor, ref string) (Ticket, error) {
	return Ticket{}, errNotImplemented
}
func (t *ticketService) List(ctx context.Context, actor Actor, projectRef string, f TicketFilter) (TicketPage, error) {
	return TicketPage{}, errNotImplemented
}
func (t *ticketService) Update(ctx context.Context, actor Actor, ref string, in UpdateTicketInput) (Ticket, error) {
	return Ticket{}, errNotImplemented
}
func (t *ticketService) Move(ctx context.Context, actor Actor, ref string, in MoveInput) (MoveResult, error) {
	return MoveResult{}, errNotImplemented
}
func (t *ticketService) Delete(ctx context.Context, actor Actor, ref string) error {
	return errNotImplemented
}
func (t *ticketService) Resolve(ctx context.Context, actor Actor, ref string) (TicketRef, error) {
	return TicketRef{}, errNotImplemented
}
