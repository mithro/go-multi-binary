package teleport

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/mithro/go-multi-binary/internal/fatblob"
)

// fakeTransport records what Deploy does without touching a real host.
type fakeTransport struct {
	uname   string
	putData map[string][]byte
	ran     []string
	// remoteMD5 lets the fake answer a `md5sum` verification command.
	answerMD5 bool
}

func newFake(uname string) *fakeTransport {
	return &fakeTransport{uname: uname, putData: map[string][]byte{}, answerMD5: true}
}

func (f *fakeTransport) RunUname(ctx context.Context) (string, error) { return f.uname, nil }

func (f *fakeTransport) Put(ctx context.Context, data []byte, remotePath string) error {
	f.putData[remotePath] = append([]byte(nil), data...)
	return nil
}

func (f *fakeTransport) Run(ctx context.Context, cmd string) (string, error) {
	f.ran = append(f.ran, cmd)
	// Emulate `md5sum <path>` by returning the md5 of whatever we stored there.
	if f.answerMD5 && strings.HasPrefix(cmd, "md5sum ") {
		path := strings.TrimSpace(strings.TrimPrefix(cmd, "md5sum "))
		if data, ok := f.putData[path]; ok {
			sum := md5.Sum(data)
			return hex.EncodeToString(sum[:]) + "  " + path, nil
		}
	}
	return "", nil
}

func syntheticImage(t *testing.T) []byte {
	t.Helper()
	blob := fatblob.Blob{Slices: []fatblob.Slice{
		{Arch: "386", Data: []byte("\x7fELF-386-native")},
		{Arch: "amd64", Data: []byte("\x7fELF-amd64-native")},
		{Arch: "arm", Data: []byte("\x7fELF-arm-native")},
		{Arch: "arm64", Data: []byte("\x7fELF-arm64-native")},
		{Arch: "riscv64", Data: []byte("\x7fELF-rv64-native")},
		{Arch: "riscv32", Status: fatblob.StatusReserved},
	}}
	img, err := fatblob.BuildCanonical([]byte("\x7fELF-amd64-native"), blob)
	if err != nil {
		t.Fatalf("BuildCanonical: %v", err)
	}
	return img
}

func TestDeployInstallsReconstructedArch(t *testing.T) {
	img := syntheticImage(t)
	f := newFake("riscv64")

	res, err := Deploy(context.Background(), img, f, "~/local/bin")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res.Arch != "riscv64" {
		t.Fatalf("res.Arch = %q, want riscv64", res.Arch)
	}
	wantPath := "~/local/bin/go-teleport-self"
	got, ok := f.putData[wantPath]
	if !ok {
		t.Fatalf("nothing installed at %s (put paths: %v)", wantPath, keys(f.putData))
	}
	// The installed bytes must equal reconstruct(img, riscv64) exactly (HR4).
	want, err := fatblob.Reconstruct(img, "riscv64")
	if err != nil {
		t.Fatal(err)
	}
	if hexMD5(got) != hexMD5(want) {
		t.Fatalf("installed md5 %s != reconstruct(riscv64) md5 %s", hexMD5(got), hexMD5(want))
	}
	if res.MD5 != hexMD5(want) {
		t.Fatalf("Result.MD5 %s != %s", res.MD5, hexMD5(want))
	}
}

func TestDeployChmodsAndVerifies(t *testing.T) {
	f := newFake("arm64")
	if _, err := Deploy(context.Background(), syntheticImage(t), f, "~/local/bin"); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	var sawChmod, sawMkdir, sawMD5 bool
	for _, c := range f.ran {
		if strings.Contains(c, "chmod") {
			sawChmod = true
		}
		if strings.Contains(c, "mkdir") {
			sawMkdir = true
		}
		if strings.HasPrefix(c, "md5sum") {
			sawMD5 = true
		}
	}
	if !sawMkdir || !sawChmod || !sawMD5 {
		t.Fatalf("expected mkdir+chmod+md5sum; ran=%v", f.ran)
	}
}

func TestDeployRejectsUnsupportedRemote(t *testing.T) {
	f := newFake("sparc64")
	if _, err := Deploy(context.Background(), syntheticImage(t), f, "~/local/bin"); err == nil {
		t.Fatalf("expected error for unsupported remote arch")
	}
}

func TestDeployFailsOnMD5Mismatch(t *testing.T) {
	f := newFake("riscv64")
	f.answerMD5 = false // remote returns empty md5 -> verification must fail
	if _, err := Deploy(context.Background(), syntheticImage(t), f, "~/local/bin"); err == nil {
		t.Fatalf("expected md5 verification failure")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hexMD5(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}
