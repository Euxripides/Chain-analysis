package secret

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "provider-secrets.json")
	return NewManager(ManagerOptions{FilePath: path}), path
}

func TestResolveEnvRef(t *testing.T) {
	m, _ := newTestManager(t)
	t.Setenv("DRPC_TEST_KEY", "abcd1234567890")

	got, err := m.Resolve(context.Background(), "env://DRPC_TEST_KEY")
	if err != nil || got != "abcd1234567890" {
		t.Fatalf("Resolve(env) = %q, %v; want the env value", got, err)
	}

	status := m.Describe(context.Background(), "env://DRPC_TEST_KEY")
	if status.Kind != KindEnv || !status.Configured || status.Name != "DRPC_TEST_KEY" {
		t.Fatalf("unexpected env status: %+v", status)
	}
	if status.Prefix != "abcd12" {
		t.Fatalf("prefix = %q, want the first 6 characters", status.Prefix)
	}
}

func TestResolveNotConfiguredCases(t *testing.T) {
	m, path := newTestManager(t)
	ctx := context.Background()

	// 未设置的 env、显式置空的 env、空引用、store 中不存在的键：都必须是 ErrNotConfigured。
	if _, err := m.Resolve(ctx, "env://DRPC_TEST_MISSING"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing env: err = %v, want ErrNotConfigured", err)
	}
	t.Setenv("DRPC_TEST_EMPTY", "")
	if _, err := m.Resolve(ctx, "env://DRPC_TEST_EMPTY"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("empty env: err = %v, want ErrNotConfigured", err)
	}
	if _, err := m.Resolve(ctx, ""); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("empty ref: err = %v, want ErrNotConfigured", err)
	}
	if _, err := m.Resolve(ctx, "   "); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("blank ref: err = %v, want ErrNotConfigured", err)
	}
	if _, err := m.Resolve(ctx, "store://drpc/runtime_api_key"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing store key (file absent): err = %v, want ErrNotConfigured", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only resolve must not create the secrets file: stat err = %v", err)
	}
	if empty := m.Describe(ctx, ""); empty.Kind != KindEmpty || empty.Configured {
		t.Fatalf("empty ref status = %+v, want kind empty and not configured", empty)
	}
}

func TestResolveRejectsInvalidRefs(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()

	// 非法引用必须是明确错误，不能被当成"未配置"而静默回退。
	for _, ref := range []string{"vault://secret/drpc", "store://drpc", "store://drpc/", "store:///runtime_api_key", "store://drpc/a/b", "env://"} {
		_, err := m.Resolve(ctx, ref)
		if err == nil {
			t.Fatalf("Resolve(%q) unexpectedly succeeded", ref)
		}
		if errors.Is(err, ErrNotConfigured) {
			t.Fatalf("Resolve(%q) must not report ErrNotConfigured, got %v", ref, err)
		}
		if status := m.Describe(ctx, ref); status.Configured {
			t.Fatalf("Describe(%q) must report not configured", ref)
		}
	}
	if status := m.Describe(ctx, "vault://secret/drpc"); status.Kind != KindUnknown {
		t.Fatalf("unknown scheme kind = %q, want %q", status.Kind, KindUnknown)
	}
}

func TestResolveHonoursCancelledContext(t *testing.T) {
	m, _ := newTestManager(t)
	t.Setenv("DRPC_TEST_KEY", "abcd1234567890")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Resolve(ctx, "env://DRPC_TEST_KEY"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: err = %v, want context.Canceled", err)
	}
}

func TestPutResolveDeleteRoundTrip(t *testing.T) {
	m, path := newTestManager(t)
	ctx := context.Background()

	ref, status, err := m.Put("DRPC", "Runtime_API_Key", "drpc-plaintext-key")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if ref != "store://drpc/runtime_api_key" {
		t.Fatalf("ref = %q, want the normalized store ref", ref)
	}
	if !status.Configured || status.Kind != KindStore || status.Name != "drpc/runtime_api_key" {
		t.Fatalf("unexpected status after Put: %+v", status)
	}

	got, err := m.Resolve(ctx, ref)
	if err != nil || got != "drpc-plaintext-key" {
		t.Fatalf("Resolve after Put = %q, %v", got, err)
	}
	// provider/kind 段大小写与前后空白不影响解析（scheme 本身大小写敏感）。
	if got, err := m.Resolve(ctx, "  store://DRPC/Runtime_API_Key  "); err != nil {
		t.Fatalf("mixed-case provider/kind must resolve: %v", err)
	} else if got != "drpc-plaintext-key" {
		t.Fatalf("mixed-case provider/kind resolved to %q", got)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read secrets file: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("secrets file must stay valid JSON: %v", err)
	}
	if decoded[ref] != "drpc-plaintext-key" {
		t.Fatalf("secrets file content = %v, want the canonical ref key", decoded)
	}

	if err := m.Delete("drpc", "runtime_api_key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Resolve(ctx, ref); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Resolve after Delete: err = %v, want ErrNotConfigured", err)
	}
	if status := m.Describe(ctx, ref); status.Configured {
		t.Fatalf("Describe after Delete must report not configured: %+v", status)
	}
	// 幂等：重复删除不是错误（轮换流程会重跑）。
	if err := m.Delete("drpc", "runtime_api_key"); err != nil {
		t.Fatalf("second Delete must be a no-op, got %v", err)
	}
}

func TestPutFileAndDirPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	m, path := newTestManager(t)
	if _, _, err := m.Put("drpc", "runtime_api_key", "drpc-plaintext-key"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat secrets file: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("secrets file mode = %o, want 600", perm)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat secrets dir: %v", err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Fatalf("secrets dir mode = %o, want 700", perm)
	}
}

func TestDefaultFilePathUsesHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME is not consulted on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := NewManager(ManagerOptions{})
	want := filepath.Join(home, ".blockchain-platform", "provider-secrets.json")
	if m.FilePath() != want {
		t.Fatalf("default path = %q, want %q", m.FilePath(), want)
	}
	if _, _, err := m.Put("drpc", "keys_api_token", "token-value"); err != nil {
		t.Fatalf("Put with default path: %v", err)
	}
	st, err := os.Stat(want)
	if err != nil {
		t.Fatalf("default secrets file: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("default secrets file mode = %o, want 600", perm)
	}
}

func TestPutRejectsInvalidInput(t *testing.T) {
	m, _ := newTestManager(t)
	cases := []struct {
		provider, kind, plaintext string
	}{
		{"", "runtime_api_key", "value"},
		{"drpc", "", "value"},
		{"dr pc", "runtime_api_key", "value"},
		{"drpc", "runtime/key", "value"},
		{"DRPC", "runtime api key", "value"},
		{"drpc", "runtime_api_key", ""},
		{"drpc", "runtime_api_key", "   "},
	}
	for _, tc := range cases {
		if _, _, err := m.Put(tc.provider, tc.kind, tc.plaintext); err == nil {
			t.Fatalf("Put(%q, %q, %q) unexpectedly succeeded", tc.provider, tc.kind, tc.plaintext)
		}
	}
}

func TestDescribeDoesNotLeakPlaintext(t *testing.T) {
	m, _ := newTestManager(t)
	const plaintext = "SUPER-SECRET-DRPC-KEY-0123456789"
	ref, _, err := m.Put("drpc", "runtime_api_key", plaintext)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	status := m.Describe(context.Background(), ref)
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if strings.Contains(string(encoded), plaintext) {
		t.Fatalf("Describe leaked the plaintext: %s", encoded)
	}
	if status.Prefix != "SUPER-" || len(status.Prefix) != 6 {
		t.Fatalf("prefix = %q, want exactly the first 6 characters", status.Prefix)
	}
	if status.Fingerprint == "" || strings.Contains(plaintext, status.Fingerprint) {
		t.Fatalf("fingerprint %q must be a hash, not a slice of the plaintext", status.Fingerprint)
	}
	// 短明文只给出全部字符，不补齐、不报错。
	short, _, err := m.Put("drpc", "keys_api_token", "abc")
	if err != nil {
		t.Fatalf("Put short: %v", err)
	}
	if got := m.Describe(context.Background(), short).Prefix; got != "abc" {
		t.Fatalf("short prefix = %q, want %q", got, "abc")
	}
}

func TestFingerprintIsStable(t *testing.T) {
	m, _ := newTestManager(t)
	ref, status, err := m.Put("drpc", "runtime_api_key", "drpc-plaintext-key")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	wantSum := sha256.Sum256([]byte("drpc-plaintext-key"))
	want := hex.EncodeToString(wantSum[:])[:16]
	if status.Fingerprint != want {
		t.Fatalf("fingerprint = %q, want sha256 prefix %q", status.Fingerprint, want)
	}
	if len(status.Fingerprint) != 16 {
		t.Fatalf("fingerprint length = %d, want 16", len(status.Fingerprint))
	}
	// 同一明文（哪怕换个 kind）指纹必须一致；不同明文必须不同。
	_, other, err := m.Put("drpc", "keys_api_token", "drpc-plaintext-key")
	if err != nil {
		t.Fatalf("Put second kind: %v", err)
	}
	if other.Fingerprint != status.Fingerprint {
		t.Fatalf("same plaintext produced different fingerprints: %q vs %q", status.Fingerprint, other.Fingerprint)
	}
	_, different, err := m.Put("drpc", "statistics_api_token", "another-key")
	if err != nil {
		t.Fatalf("Put third kind: %v", err)
	}
	if different.Fingerprint == status.Fingerprint {
		t.Fatalf("different plaintext produced the same fingerprint")
	}
	// 重复 Describe 必须稳定（指纹来自明文，不来自时间或内存地址）。
	again := m.Describe(context.Background(), ref)
	if again.Fingerprint != status.Fingerprint || again.Prefix != status.Prefix {
		t.Fatalf("Describe is not stable: %+v vs %+v", again, status)
	}
}

func TestCorruptStoreFileSurfacesError(t *testing.T) {
	m, path := newTestManager(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	ctx := context.Background()
	_, err := m.Resolve(ctx, "store://drpc/runtime_api_key")
	if err == nil || errors.Is(err, ErrNotConfigured) {
		t.Fatalf("corrupt file must surface a parse error, got %v", err)
	}
	// 修好文件后必须能恢复（mtime 变化触发重读），不需要重启进程。
	if err := os.WriteFile(path, []byte("{\"store://drpc/runtime_api_key\": \"recovered\"}\n"), 0o600); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}
	got, err := m.Resolve(ctx, "store://drpc/runtime_api_key")
	if err != nil || got != "recovered" {
		t.Fatalf("Resolve after repair = %q, %v", got, err)
	}
}

func TestManagerObservesExternalWrites(t *testing.T) {
	m, path := newTestManager(t)
	writer := NewManager(ManagerOptions{FilePath: path})
	ctx := context.Background()

	// 先让 reader 建立"文件不存在"的缓存，再模拟另一个进程写入。
	if _, err := m.Resolve(ctx, "store://drpc/runtime_api_key"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("precondition failed: err = %v", err)
	}
	ref, _, err := writer.Put("drpc", "runtime_api_key", "written-by-another-process")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := m.Resolve(ctx, ref)
	if err != nil || got != "written-by-another-process" {
		t.Fatalf("reader did not observe the external write: %q, %v", got, err)
	}
}

func TestLooseFilePermissionsAreTightened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	m, path := newTestManager(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{\"store://drpc/runtime_api_key\": \"v\"}\n"), 0o644); err != nil {
		t.Fatalf("write loose file: %v", err)
	}
	if _, err := m.Resolve(context.Background(), "store://drpc/runtime_api_key"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode after load = %o, want 600", perm)
	}
}

func TestBareStoreKeyIsAccepted(t *testing.T) {
	m, path := newTestManager(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 手工维护的文件允许省略 scheme，避免"运维写对了却报未配置"。
	if err := os.WriteFile(path, []byte("{\"drpc/runtime_api_key\": \"hand-written\"}\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	got, err := m.Resolve(context.Background(), "store://drpc/runtime_api_key")
	if err != nil || got != "hand-written" {
		t.Fatalf("Resolve(bare key) = %q, %v", got, err)
	}
	if err := m.Delete("drpc", "runtime_api_key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Resolve(context.Background(), "store://drpc/runtime_api_key"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("bare key survived Delete: err = %v", err)
	}
}

func TestConcurrentPutAndResolve(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()
	const (
		writers = 8
		readers = 8
		rounds  = 20
	)

	var wg sync.WaitGroup
	errs := make(chan error, writers*rounds+readers*rounds)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < rounds; n++ {
				kind := "kind-" + string(rune('a'+id))
				if _, _, err := m.Put("drpc", kind, "value-"+kind); err != nil {
					errs <- err
				}
			}
		}(i)
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < rounds; n++ {
				if _, err := m.Resolve(ctx, "store://drpc/kind-a"); err != nil && !errors.Is(err, ErrNotConfigured) {
					errs <- err
				}
				_ = m.Describe(ctx, "store://drpc/kind-b")
				_ = m.FilePath()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent access failed: %v", err)
	}
	// 并发写入不能互相覆盖：每个 writer 的键最终都必须能解析。
	for i := 0; i < writers; i++ {
		kind := "kind-" + string(rune('a'+i))
		got, err := m.Resolve(ctx, "store://drpc/"+kind)
		if err != nil || got != "value-"+kind {
			t.Fatalf("Resolve(%s) = %q, %v; concurrent Put lost a write", kind, got, err)
		}
	}
}
