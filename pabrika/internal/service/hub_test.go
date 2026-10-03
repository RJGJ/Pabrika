package service_test

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/RJGJ/Pabrika/internal/service"
	"github.com/RJGJ/Pabrika/internal/testutil"
)

func hubEvent(project string, n int) service.Event {
	return service.Event{
		Type: service.EventTicketUpdated, ProjectID: project, TicketID: fmt.Sprintf("T%d", n),
		Actor: service.EventActor{Type: service.ActorUser, ID: "u"},
		At:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func mustSub(t *testing.T, h *service.Hub, project, user string) *service.Subscription {
	t.Helper()
	s, err := h.Subscribe(project, user)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func isDone(s *service.Subscription) bool {
	select {
	case <-s.Done():
		return true
	default:
		return false
	}
}

func TestHubIsolationAndOrder(t *testing.T) {
	h := service.NewHub(service.HubOptions{})
	a1, a2, b := mustSub(t, h, "A", "u1"), mustSub(t, h, "A", "u2"), mustSub(t, h, "B", "u3")
	for i := 0; i < 10; i++ {
		h.Publish(hubEvent("A", i))
	}
	for _, s := range []*service.Subscription{a1, a2} {
		for i := 0; i < 10; i++ {
			select {
			case e := <-s.C():
				if e.TicketID != fmt.Sprintf("T%d", i) {
					t.Fatalf("order: got %s want T%d", e.TicketID, i)
				}
			default:
				t.Fatalf("missing event %d", i)
			}
		}
	}
	select {
	case e := <-b.C():
		t.Fatalf("project B received %v", e)
	default:
	}
	if h.SubscriberCount("A") != 2 || h.SubscriberCount("B") != 1 || h.SubscriberCount("Z") != 0 {
		t.Fatal("bad counts")
	}
}

func TestHubSlowClient(t *testing.T) {
	const buf = 4
	h := service.NewHub(service.HubOptions{StreamBuffer: buf})
	slow, fast := mustSub(t, h, "A", "slow"), mustSub(t, h, "A", "fast")
	var got []service.Event
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for len(got) < buf+1 {
			select {
			case e := <-fast.C():
				got = append(got, e)
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()
	pubDone := make(chan struct{})
	go func() {
		defer close(pubDone)
		for i := 0; i < buf+1; i++ {
			h.Publish(hubEvent("A", i))
			time.Sleep(time.Millisecond)
		}
	}()
	select {
	case <-pubDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked")
	}
	<-finished
	if len(got) != buf+1 {
		t.Fatalf("fast subscriber got %d events", len(got))
	}
	if !isDone(slow) || slow.Reason() != service.CloseSlow {
		t.Fatalf("slow not dropped: reason %q", slow.Reason())
	}
	if isDone(fast) {
		t.Fatal("fast dropped")
	}
	if h.SubscriberCount("A") != 1 {
		t.Fatalf("count %d", h.SubscriberCount("A"))
	}
}

func TestHubTargetedCloses(t *testing.T) {
	h := service.NewHub(service.HubOptions{})
	u1a, u1b := mustSub(t, h, "A", "u1"), mustSub(t, h, "A", "u1")
	u2, other := mustSub(t, h, "A", "u2"), mustSub(t, h, "B", "u1")
	h.CloseUser("A", "u1", service.CloseRemoved)
	for _, s := range []*service.Subscription{u1a, u1b} {
		if !isDone(s) || s.Reason() != service.CloseRemoved {
			t.Fatal("u1 on A not closed with CloseRemoved")
		}
	}
	if isDone(u2) || isDone(other) {
		t.Fatal("CloseUser closed too much")
	}
	h.CloseProject("A", service.CloseProject)
	if !isDone(u2) || u2.Reason() != service.CloseProject || isDone(other) {
		t.Fatal("CloseProject wrong")
	}
	if h.SubscriberCount("A") != 0 {
		t.Fatal("A not empty")
	}
	h.Shutdown()
	if !isDone(other) || other.Reason() != service.CloseShutdown {
		t.Fatal("Shutdown did not close")
	}
	if _, err := h.Subscribe("A", "u"); err != service.ErrHubClosed {
		t.Fatalf("Subscribe after Shutdown: %v", err)
	}
}

func TestHubIdempotence(t *testing.T) {
	h := service.NewHub(service.HubOptions{})
	s := mustSub(t, h, "A", "u")
	s.Close()
	s.Close()
	if s.Reason() != service.CloseClient {
		t.Fatalf("reason %q", s.Reason())
	}
	s2 := mustSub(t, h, "A", "u")
	h.CloseUser("A", "u", service.CloseRemoved)
	h.CloseUser("A", "u", service.CloseSlow)
	if s2.Reason() != service.CloseRemoved {
		t.Fatal("first reason must win")
	}
	s2.Close()
	h.Shutdown()
	h.Shutdown()
	s2.Close()
	if s2.Reason() != service.CloseRemoved {
		t.Fatal("later calls changed the reason")
	}
	// C() must stay open: a closed channel would yield immediately.
	select {
	case _, ok := <-s2.C():
		t.Fatalf("C() yielded (ok=%v) on an empty channel", ok)
	default:
	}
	h.Publish(hubEvent("A", 1))
	h.CloseProject("none", service.CloseProject)
}

func TestEventJSONKeys(t *testing.T) {
	full := service.Event{
		Type: service.EventTicketMoved, ProjectID: "p", TicketID: "t", CommentID: "c", LabelID: "l",
		UserID: "u", Renumbered: true, Actor: service.EventActor{Type: service.ActorAPIToken, ID: "a"},
		At: time.Now().UTC(),
	}
	keys := func(v any) map[string]bool {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for k := range m {
			out[k] = true
		}
		return out
	}
	want := []string{"type", "project_id", "ticket_id", "comment_id", "label_id", "user_id", "renumbered", "actor", "at"}
	got := keys(full)
	if len(got) != len(want) {
		t.Fatalf("keys %v", got)
	}
	for _, k := range want {
		if !got[k] {
			t.Fatalf("missing key %s", k)
		}
	}
	min := keys(service.Event{Type: service.EventProjectUpdated, ProjectID: "p"})
	if len(min) != 4 || !min["type"] || !min["project_id"] || !min["actor"] || !min["at"] {
		t.Fatalf("minimal keys %v", min)
	}
	if a := keys(full.Actor); len(a) != 2 || !a["type"] || !a["id"] {
		t.Fatalf("actor keys %v", a)
	}
}

func TestWithHubOption(t *testing.T) {
	env := testutil.NewTestServices(t, testutil.WithHub(service.HubOptions{StreamBuffer: 3}))
	if env.Hub == nil {
		t.Fatal("env.Hub is nil")
	}
	sub := mustSub(t, env.Hub, "P", "u")
	env.Hub.Publish(hubEvent("P", 1))
	if len(sub.C()) != 1 {
		t.Fatal("hub from env does not deliver")
	}
	if testutil.NewTestServices(t).Hub != nil {
		t.Fatal("default env must not have a hub")
	}
}

func TestHubStress(t *testing.T) {
	h := service.NewHub(service.HubOptions{StreamBuffer: 2})
	projects := []string{"A", "B", "C"}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	spawn := func(f func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					f(i)
				}
			}
		}()
	}
	for g := 0; g < 4; g++ {
		spawn(func(i int) { h.Publish(hubEvent(projects[i%3], i)) })
	}
	for g := 0; g < 4; g++ {
		spawn(func(i int) {
			s, err := h.Subscribe(projects[i%3], fmt.Sprintf("u%d", i%4))
			if err != nil {
				return
			}
			select {
			case <-s.C():
			default:
			}
			if i%2 == 0 {
				s.Close()
			}
		})
	}
	spawn(func(i int) { h.CloseUser(projects[i%3], fmt.Sprintf("u%d", i%4), service.CloseRemoved) })
	spawn(func(i int) { h.CloseProject(projects[i%3], service.CloseProject) })
	spawn(func(i int) { _ = h.SubscriberCount(projects[i%3]) })

	time.Sleep(300 * time.Millisecond)
	h.Shutdown()
	time.Sleep(50 * time.Millisecond)
	close(stop)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: workers did not finish")
	}
	// Subscribes racing Shutdown are rejected, so nothing may remain registered.
	for _, p := range projects {
		if n := h.SubscriberCount(p); n != 0 {
			t.Fatalf("project %s leaked %d subscriptions", p, n)
		}
	}
	if _, err := h.Subscribe("A", "u"); err != service.ErrHubClosed {
		t.Fatalf("got %v", err)
	}
}
