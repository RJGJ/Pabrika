package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Params are the argon2id cost parameters. Memory is in KiB.
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	KeyLen  uint32
}

var (
	// DefaultParams are the production parameters (64 MiB, 3 passes, 2 lanes, 32-byte key).
	// The CLI and the server both use them: this is the one source of truth.
	DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 2, KeyLen: 32}
	// TestParams are cheap parameters for tests; only one test uses DefaultParams.
	TestParams = Params{Memory: 8, Time: 1, Threads: 1, KeyLen: 32}
)

const (
	saltLen = 16
	// DefaultConcurrency bounds simultaneous argon2 computations (64 MiB each).
	DefaultConcurrency = 4

	// MinPasswordLen and MaxPasswordLen count characters (runes).
	MinPasswordLen = 10
	MaxPasswordLen = 200

	// Sanity bounds applied to parameters parsed from a stored hash.
	maxVerifyMemory  = 1 << 20 // KiB (1 GiB)
	maxVerifyTime    = 64
	maxVerifyThreads = 64
)

// PasswordProblem returns a user-facing message when pw breaks the password rules, else "".
func PasswordProblem(pw string) string {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinPasswordLen:
		return fmt.Sprintf("must be at least %d characters", MinPasswordLen)
	case n > MaxPasswordLen:
		return fmt.Sprintf("must be at most %d characters", MaxPasswordLen)
	}
	return ""
}

// Hasher hashes and verifies passwords with argon2id. A semaphore bounds concurrent hashes
// so argon2 memory stays bounded; acquiring it respects the request context.
type Hasher struct {
	params Params
	sem    chan struct{}

	dummyOnce sync.Once
	dummy     string
}

// NewHasher builds a Hasher. concurrency <= 0 means DefaultConcurrency.
func NewHasher(p Params, concurrency int) *Hasher {
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	return &Hasher{params: p, sem: make(chan struct{}, concurrency)}
}

// Params returns the parameters used for new hashes.
func (h *Hasher) Params() Params { return h.params }

func (h *Hasher) acquire(ctx context.Context) (release func(), err error) {
	// A context that is already done must never win a free slot.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// derive is the only argon2.IDKey call site.
func derive(pw string, salt []byte, memory, time uint32, threads uint8, keyLen uint32) []byte {
	return argon2.IDKey([]byte(pw), salt, time, memory, threads, keyLen)
}

// Hash returns the PHC string of pw using a fresh random salt. It does not apply the password
// rules (see PasswordProblem).
func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := derive(pw, salt, h.params.Memory, h.params.Time, h.params.Threads, h.params.KeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		h.params.Memory, h.params.Time, h.params.Threads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// ErrMalformedHash means a stored hash is not a valid argon2id PHC string.
var ErrMalformedHash = errors.New("auth: malformed password hash")

// Verify checks pw against a PHC string. The cost parameters come from the stored string,
// never from configuration. A mismatch is (false, nil); a malformed hash is ErrMalformedHash.
func (h *Hasher) Verify(ctx context.Context, pw, phc string) (bool, error) {
	memory, time, threads, salt, want, err := parsePHC(phc)
	if err != nil {
		return false, err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	got := derive(pw, salt, memory, time, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func parsePHC(phc string) (memory, time uint32, threads uint8, salt, key []byte, err error) {
	bad := func() (uint32, uint32, uint8, []byte, []byte, error) { return 0, 0, 0, nil, nil, ErrMalformedHash }
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return bad()
	}
	var vals [3]uint64
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return bad()
	}
	for i, want := range []string{"m=", "t=", "p="} {
		if !strings.HasPrefix(params[i], want) {
			return bad()
		}
		v, perr := strconv.ParseUint(params[i][2:], 10, 32)
		if perr != nil {
			return bad()
		}
		vals[i] = v
	}
	m, t, p := vals[0], vals[1], vals[2]
	if m < 8 || m > maxVerifyMemory || t < 1 || t > maxVerifyTime || p < 1 || p > maxVerifyThreads {
		return bad()
	}
	salt, serr := base64.RawStdEncoding.DecodeString(parts[4])
	key, kerr := base64.RawStdEncoding.DecodeString(parts[5])
	if serr != nil || kerr != nil || len(salt) < 8 || len(key) < 16 || len(key) > 1024 {
		return bad()
	}
	return uint32(m), uint32(t), uint8(p), salt, key, nil
}

// DummyVerify burns one verification's worth of time against a hash of a random value
// (computed once, with the Hasher's own parameters). Login calls it for unknown emails so
// timing does not reveal whether the account exists. It returns only context or hashing errors.
func (h *Hasher) DummyVerify(ctx context.Context, pw string) error {
	var herr error
	h.dummyOnce.Do(func() {
		random := make([]byte, 32)
		_, _ = rand.Read(random)
		h.dummy, herr = h.Hash(context.Background(), base64.RawURLEncoding.EncodeToString(random))
	})
	if herr != nil {
		return herr
	}
	_, err := h.Verify(ctx, pw, h.dummy)
	return err
}
