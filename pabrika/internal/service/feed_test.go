package service_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func feedAll(t *testing.T, e *tkEnv, actor service.Actor, project string, f service.FeedFilter) []service.FeedItem {
	t.Helper()
	var out []service.FeedItem
	f.Limit = 3
	for i := 0; i < 100; i++ {
		p, err := e.Svc.Activity.ListProject(ctx, actor, project, f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Items...)
		if p.NextCursor == "" {
			return out
		}
		f.Cursor = p.NextCursor
	}
	t.Fatal("cursor loop")
	return nil
}

func TestProjectFeedOrderKindsAndCursor(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "A", service.StatusTodo) // activity: created
	e.Clock.Advance(time.Second)
	if _, err := e.Svc.Comments.Add(ctx, e.Owner, tk.Ref, strings.Repeat("é", 300)); err != nil {
		t.Fatal(err)
	}
	// several rows with the same timestamp
	for i := 0; i < 4; i++ {
		if _, err := e.Svc.Comments.Add(ctx, e.Editor, tk.Ref, "same instant"); err != nil {
			t.Fatal(err)
		}
	}
	e.Clock.Advance(time.Second)
	lid := e.label(t, e.P.ID, service.LabelAwaitingPlanApproval)
	e.change(t, tk.Ref, []string{lid}, nil)
	e.Clock.Advance(time.Second)
	if _, err := e.Svc.Decisions.Record(ctx, e.Owner, tk.Ref, approve(service.GatePlan)); err != nil {
		t.Fatal(err)
	}

	items := feedAll(t, e, e.Viewer, e.P.Key, service.FeedFilter{})
	if len(items) != 8 { // created, 5 comments, labeled, decision
		t.Fatalf("got %d items", len(items))
	}
	seen := map[string]bool{}
	for i, it := range items {
		if seen[it.ID] {
			t.Fatalf("duplicate %s across pages", it.ID)
		}
		seen[it.ID] = true
		if i > 0 && (items[i-1].CreatedAt.Before(it.CreatedAt) ||
			(items[i-1].CreatedAt.Equal(it.CreatedAt) && items[i-1].ID <= it.ID)) {
			t.Fatalf("order broken at %d", i)
		}
		if it.TicketRef != tk.Ref || it.Title != "A" || it.TicketID != tk.ID {
			t.Fatalf("ticket: %+v", it)
		}
	}
	if items[0].Kind != service.FeedKindDecision || items[0].Action != "decision" || items[0].Changes["gate_name"][1] != "plan" {
		t.Fatalf("first: %+v", items[0])
	}
	if items[1].Kind != service.FeedKindActivity || items[1].Action != "labeled" {
		t.Fatalf("second: %+v", items[1])
	}
	var long *service.FeedItem
	for i := range items {
		if items[i].Kind == service.FeedKindComment && items[i].Actor.ID == e.M.Users[testutil.Owner].ID {
			long = &items[i]
		}
	}
	if long == nil || len([]rune(long.Summary)) != 200 || long.Action != "commented" {
		t.Fatalf("truncation: %+v", long)
	}

	// filters
	if got := feedAll(t, e, e.Viewer, e.P.Key, service.FeedFilter{Kind: "comment"}); len(got) != 5 {
		t.Fatalf("kind comment: %d", len(got))
	}
	if got := feedAll(t, e, e.Viewer, e.P.Key, service.FeedFilter{Kind: "decision"}); len(got) != 1 {
		t.Fatalf("kind decision: %d", len(got))
	}
	if got := feedAll(t, e, e.Viewer, e.P.Key, service.FeedFilter{Kind: "activity"}); len(got) != 2 {
		t.Fatalf("kind activity: %d", len(got))
	}
	if got := feedAll(t, e, e.Viewer, e.P.Key, service.FeedFilter{ActorType: "api_token"}); len(got) != 0 {
		t.Fatalf("actor token: %d", len(got))
	}
	if got := feedAll(t, e, e.Viewer, e.P.Key, service.FeedFilter{Ticket: tk.Ref}); len(got) != 8 {
		t.Fatalf("ticket filter: %d", len(got))
	}
	_, err := e.Svc.Activity.ListProject(ctx, e.Viewer, e.P.Key, service.FeedFilter{Kind: "nope"})
	wantCode(t, err, service.KindValidation, "")
}

func TestProjectFeedAuthIsolationAndSoftDelete(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "A", service.StatusTodo)
	cm, _ := e.Svc.Comments.Add(ctx, e.Owner, tk.Ref, "keep")
	gone, _ := e.Svc.Comments.Add(ctx, e.Owner, tk.Ref, "gone")
	other := e.create(t, "B", service.StatusTodo)

	// a second project with its own ticket
	m2 := e.NewMatrix(t, "OTH")
	o2 := e.UserActor(m2.Users[testutil.Owner])
	t2, err := e.Svc.Tickets.Create(ctx, o2, "OTH", service.CreateTicketInput{Title: "other project"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = e.Svc.Activity.ListProject(ctx, e.UserActor(e.M.Users[testutil.NonMember]), e.P.Key, service.FeedFilter{})
	wantCode(t, err, service.KindNotFound, "")
	_, err = e.Svc.Activity.ListProject(ctx, e.Owner, "OTH", service.FeedFilter{})
	wantCode(t, err, service.KindNotFound, "")
	rt := e.TokenActor(e.NewToken(t, e.M.Users[testutil.Owner].ID, service.ScopeRead, ""))
	if _, err := e.Svc.Activity.ListProject(ctx, rt, e.P.Key, service.FeedFilter{}); err != nil {
		t.Fatalf("read token: %v", err)
	}
	limited := e.TokenActor(e.NewToken(t, e.M.Users[testutil.Owner].ID, service.ScopeRead, e.P.ID))
	_, err = e.Svc.Activity.ListProject(ctx, limited, "WEB", service.FeedFilter{})
	if err != nil {
		t.Fatal(err)
	}

	for _, it := range feedAll(t, e, e.Owner, e.P.Key, service.FeedFilter{}) {
		if it.TicketID == t2.ID {
			t.Fatal("row of another project")
		}
	}
	// filtering by a ticket of another project is a validation error
	_, err = e.Svc.Activity.ListProject(ctx, e.Owner, e.P.Key, service.FeedFilter{Ticket: t2.Ref})
	wantCode(t, err, service.KindValidation, "")

	// comment edit reflected, delete hides, ticket delete hides all its rows
	if _, err := e.Svc.Comments.Edit(ctx, e.Owner, cm.ID, "edited text"); err != nil {
		t.Fatal(err)
	}
	if err := e.Svc.Comments.Delete(ctx, e.Owner, gone.ID); err != nil {
		t.Fatal(err)
	}
	items := feedAll(t, e, e.Owner, e.P.Key, service.FeedFilter{Kind: "comment"})
	if len(items) != 1 || items[0].Summary != "edited text" {
		t.Fatalf("comments: %+v", items)
	}
	if err := e.Svc.Tickets.Delete(ctx, e.Owner, other.Ref); err != nil {
		t.Fatal(err)
	}
	for _, it := range feedAll(t, e, e.Owner, e.P.Key, service.FeedFilter{}) {
		if it.TicketID == other.ID {
			t.Fatalf("soft-deleted ticket leaked: %+v", it)
		}
	}
}

func TestProjectFeedBotActor(t *testing.T) {
	e := newTk(t)
	tk := e.create(t, "A", service.StatusTodo)
	bot := e.botActor(t, "dev")
	if _, err := e.Svc.Comments.Add(ctx, bot, tk.Ref, "[developer] PLAN"); err != nil {
		t.Fatal(err)
	}
	got := feedAll(t, e, e.Owner, e.P.Key, service.FeedFilter{ActorType: "api_token"})
	if len(got) != 1 || got[0].Actor.Type != service.ActorAPIToken || got[0].Actor.OwnerName == "" || got[0].Summary != "[developer] PLAN" {
		t.Fatalf("%+v", got)
	}
}
