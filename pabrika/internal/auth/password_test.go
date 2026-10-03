package auth_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/RJGJ/Pabrika/internal/auth"
)

var ctx = context.Background()

func testHasher() *auth.Hasher { return auth.NewHasher(auth.TestParams, 4) }

// The one test that pays for production argon2 parameters (about 64 MiB, so never parallel).
func TestDefaultParamsPHC(t *testing.T) {
	h := auth.NewHasher(auth.DefaultParams, 1)
	phc, err := h.Hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("phc = %q", phc)
	}
	if ok, err := h.Verify(ctx, "correct horse battery", phc); err != nil || !ok {
		t.Fatalf("verify = %v %v", ok, err)
	}
	if ok, _ := h.Verify(ctx, "wrong", phc); ok {
		t.Fatal("wrong password verified")
	}
}

func TestHashVerifyRoundTrip(t *testing.T) {
	h := testHasher()
	phc, err := h.Hash(ctx, "a-long-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Fatalf("phc = %q", phc)
	}
	if ok, err := h.Verify(ctx, "a-long-password", phc); err != nil || !ok {
		t.Fatalf("verify = %v %v", ok, err)
	}
	for _, bad := range []string{"", "a-long-passworD", "a-long-password "} {
		if ok, err := h.Verify(ctx, bad, phc); err != nil || ok {
			t.Fatalf("verify(%q) = %v %v", bad, ok, err)
		}
	}
	// fresh salt every time
	phc2, _ := h.Hash(ctx, "a-long-password")
	if phc2 == phc {
		t.Fatal("two hashes of the same password are identical")
	}
}

// Verify must honour the parameters stored in the hash, not its own configuration.
func TestVerifyUsesStoredParams(t *testing.T) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	key := argon2.IDKey([]byte("another-password"), salt, 2, 16, 2, 24)
	enc := base64.RawStdEncoding
	phc := fmt.Sprintf("$argon2id$v=19$m=16,t=2,p=2$%s$%s", enc.EncodeToString(salt), enc.EncodeToString(key))
	// the hasher is configured with different parameters
	h := auth.NewHasher(auth.Params{Memory: 8, Time: 1, Threads: 1, KeyLen: 32}, 1)
	if ok, err := h.Verify(ctx, "another-password", phc); err != nil || !ok {
		t.Fatalf("verify = %v %v", ok, err)
	}
	if ok, _ := h.Verify(ctx, "nope-nope-nope", phc); ok {
		t.Fatal("wrong password verified")
	}
}

func TestVerifyMalformedHash(t *testing.T) {
	h := testHasher()
	good, _ := h.Hash(ctx, "a-long-password")
	parts := strings.Split(good, "$")
	cases := map[string]string{
		"empty":         "",
		"placeholder":   "placeholder-not-a-real-hash",
		"wrong algo":    strings.Replace(good, "argon2id", "argon2i", 1),
		"wrong version": strings.Replace(good, "v=19", "v=16", 1),
		"missing part":  strings.Join(parts[:5], "$"),
		"bad params":    strings.Replace(good, "m=8,t=1,p=1", "m=8,t=1", 1),
		"huge memory":   strings.Replace(good, "m=8,", "m=4000000000,", 1),
		"zero time":     strings.Replace(good, "t=1", "t=0", 1),
		"bad salt":      strings.Replace(good, parts[4], "!!!", 1),
		"bad key":       strings.Replace(good, parts[5], "!!!", 1),
	}
	for name, phc := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := h.Verify(ctx, "a-long-password", phc)
			if ok || !errors.Is(err, auth.ErrMalformedHash) {
				t.Fatalf("got %v %v", ok, err)
			}
		})
	}
}

func TestPasswordRules(t *testing.T) {
	cases := []struct {
		pw string
		ok bool
	}{
		{"", false},
		{strings.Repeat("a", 9), false},
		{strings.Repeat("a", 10), true},
		{strings.Repeat("é", 10), true}, // counted in characters, not bytes
		{strings.Repeat("é", 9), false},
		{strings.Repeat("a", 200), true},
		{strings.Repeat("a", 201), false},
		{strings.Repeat("日", 200), true},
		{strings.Repeat("日", 201), false},
	}
	for _, c := range cases {
		if got := auth.PasswordProblem(c.pw) == ""; got != c.ok {
			t.Errorf("PasswordProblem(%d runes) ok=%v, want %v", len([]rune(c.pw)), got, c.ok)
		}
	}
}

func TestDummyVerify(t *testing.T) {
	h := testHasher()
	if err := h.DummyVerify(ctx, "whatever"); err != nil {
		t.Fatal(err)
	}
	if err := h.DummyVerify(ctx, "whatever"); err != nil {
		t.Fatal(err)
	}
}

func TestSemaphoreRespectsContext(t *testing.T) {
	h := auth.NewHasher(auth.TestParams, 1)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := h.Hash(cancelled, "a-long-password"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Hash with cancelled ctx = %v", err)
	}
	phc, _ := h.Hash(ctx, "a-long-password")
	if _, err := h.Verify(cancelled, "a-long-password", phc); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify with cancelled ctx = %v", err)
	}
	if err := h.DummyVerify(cancelled, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DummyVerify with cancelled ctx = %v", err)
	}
}

// A waiter blocked on a full semaphore is released when its context ends.
func TestSemaphoreWaiterCancelled(t *testing.T) {
	h := auth.NewHasher(auth.Params{Memory: 64 * 1024, Time: 8, Threads: 1, KeyLen: 32}, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = h.Hash(ctx, "a-long-password") // holds the only slot for a while
	}()
	<-started
	time.Sleep(20 * time.Millisecond)
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	begin := time.Now()
	_, err := h.Hash(short, "second-password")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Skipf("first hash finished before the waiter blocked (err=%v)", err)
	}
	if time.Since(begin) > 2*time.Second {
		t.Fatal("waiter did not stop promptly")
	}
}
