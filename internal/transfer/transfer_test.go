package transfer_test

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	errs "github.com/Dmn117/ftp2sftp/internal/errors"
	"github.com/Dmn117/ftp2sftp/internal/transfer"
)

// fakeFS is an in-memory RemoteFS used to unit-test transfer orchestration
// without a real SFTP server. Behavioral coverage against a real server
// lives in tests/integration.
type fakeFS struct {
	mu        sync.Mutex
	files     map[string][]byte
	temp      map[string][]byte
	removed   []string
	commitErr error
	createErr error
}

func newFakeFS() *fakeFS {
	return &fakeFS{files: map[string][]byte{}, temp: map[string][]byte{}}
}

type fakeFileInfo struct{ name string }

func (i fakeFileInfo) Name() string       { return i.name }
func (i fakeFileInfo) Size() int64        { return 0 }
func (i fakeFileInfo) Mode() os.FileMode  { return 0o644 }
func (i fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (i fakeFileInfo) IsDir() bool        { return false }
func (i fakeFileInfo) Sys() any           { return nil }

func (f *fakeFS) Stat(p string) (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.files[p]; ok {
		return fakeFileInfo{name: p}, nil
	}

	return nil, errs.New(errs.KindNotFound, "fake.Stat", "no encontrado")
}

type fakeReadFile struct{ *bytes.Reader }

func (f *fakeReadFile) Close() error { return nil }

func (f *fakeFS) OpenRead(p string) (io.ReadSeekCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	data, ok := f.files[p]
	if !ok {
		return nil, errs.New(errs.KindNotFound, "fake.OpenRead", "no encontrado")
	}

	return &fakeReadFile{bytes.NewReader(data)}, nil
}

type fakeWriteFile struct {
	fs   *fakeFS
	path string
	buf  bytes.Buffer
}

func (w *fakeWriteFile) Write(p []byte) (int, error) { return w.buf.Write(p) }

func (w *fakeWriteFile) Close() error {
	w.fs.mu.Lock()
	defer w.fs.mu.Unlock()

	w.fs.temp[w.path] = append([]byte(nil), w.buf.Bytes()...)

	return nil
}

func (f *fakeFS) CreateTemp(p string) (io.WriteCloser, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}

	// Mirrors O_CREATE semantics of a real SFTP OpenFile: the file exists
	// as soon as it is opened, before any bytes are written or the handle
	// is closed.
	f.mu.Lock()
	f.temp[p] = nil
	f.mu.Unlock()

	return &fakeWriteFile{fs: f, path: p}, nil
}

func (f *fakeFS) Commit(tempPath, finalPath string, overwrite bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.commitErr != nil {
		return f.commitErr
	}

	if _, exists := f.files[finalPath]; exists && !overwrite {
		return errs.New(errs.KindConflict, "fake.Commit", "el archivo destino ya existe")
	}

	data, ok := f.temp[tempPath]
	if !ok {
		return errs.New(errs.KindNotFound, "fake.Commit", "temporal no encontrado")
	}

	f.files[finalPath] = data
	delete(f.temp, tempPath)

	return nil
}

func (f *fakeFS) Remove(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.temp, p)
	delete(f.files, p)
	f.removed = append(f.removed, p)

	return nil
}

func TestTempPathMatchesRF007Pattern(t *testing.T) {
	got := transfer.TempPath("/facturas/2026/archivo.xml", "sess123", ".part")
	want := "/facturas/2026/archivo.xml.part-sess123"

	if got != want {
		t.Errorf("TempPath() = %q, want %q", got, want)
	}
}

func TestUploadHappyPathCommits(t *testing.T) {
	fs := newFakeFS()

	var result transfer.Result

	released := false

	upload, err := transfer.NewUpload(fs, "/facturas/archivo.xml", transfer.UploadOptions{
		SessionID: "s1", TransferID: "t1", VirtualPath: "/archivo.xml",
		MaxSize: 1024, CalculateSHA256: true, TemporarySuffix: ".part",
		OnComplete: func(r transfer.Result) { result = r },
		Release:    func() { released = true },
	})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}

	if _, ok := fs.temp["/facturas/archivo.xml.part-s1"]; !ok {
		t.Fatalf("expected temp file to be created immediately, temp=%v", fs.temp)
	}

	if _, err := upload.Write([]byte("hello world")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := upload.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := string(fs.files["/facturas/archivo.xml"]); got != "hello world" {
		t.Errorf("final file content = %q, want %q", got, "hello world")
	}

	if _, stillTemp := fs.temp["/facturas/archivo.xml.part-s1"]; stillTemp {
		t.Error("temp file should be gone after a successful commit")
	}

	if result.Phase != transfer.PhaseCommitted {
		t.Errorf("result.Phase = %v, want PhaseCommitted", result.Phase)
	}

	if result.Bytes != int64(len("hello world")) {
		t.Errorf("result.Bytes = %d, want %d", result.Bytes, len("hello world"))
	}

	wantSHA := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9" // sha256("hello world")
	if result.SHA256 != wantSHA {
		t.Errorf("result.SHA256 = %q, want %q", result.SHA256, wantSHA)
	}

	if !released {
		t.Error("Release callback should have been called")
	}

	// Close must be idempotent: a second call must not run OnComplete again
	// or double-release.
	result = transfer.Result{}
	released = false

	if err := upload.Close(); err != nil {
		t.Fatalf("second Close should be a no-op, got: %v", err)
	}

	if result != (transfer.Result{}) || released {
		t.Error("second Close should not re-invoke OnComplete or Release")
	}
}

func TestUploadRejectsExistingFileWithoutOverwrite(t *testing.T) {
	fs := newFakeFS()
	fs.files["/archivo.xml"] = []byte("existing")

	_, err := transfer.NewUpload(fs, "/archivo.xml", transfer.UploadOptions{
		SessionID: "s1", TemporarySuffix: ".part", Overwrite: false,
	})
	if err == nil {
		t.Fatal("NewUpload should reject an existing destination when overwrite is disabled")
	}

	if errs.KindOf(err) != errs.KindConflict {
		t.Errorf("expected KindConflict, got %v", errs.KindOf(err))
	}
}

func TestUploadEnforcesMaxSizeAndCleansUpTemp(t *testing.T) {
	fs := newFakeFS()

	var result transfer.Result

	upload, err := transfer.NewUpload(fs, "/archivo.xml", transfer.UploadOptions{
		SessionID: "s1", TemporarySuffix: ".part", MaxSize: 4,
		OnComplete: func(r transfer.Result) { result = r },
	})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}

	if _, err := upload.Write([]byte("too much data")); err == nil {
		t.Fatal("Write beyond MaxSize should fail")
	}

	if err := upload.Close(); err == nil {
		t.Fatal("Close after a size violation should return the failure")
	}

	if result.Phase != transfer.PhaseFailed {
		t.Errorf("result.Phase = %v, want PhaseFailed", result.Phase)
	}

	if _, exists := fs.files["/archivo.xml"]; exists {
		t.Error("final file must not exist after a failed upload")
	}

	if len(fs.removed) == 0 {
		t.Error("temp file should have been removed after failure")
	}
}

func TestUploadTransferErrorTriggersCleanup(t *testing.T) {
	fs := newFakeFS()

	var result transfer.Result

	upload, err := transfer.NewUpload(fs, "/archivo.xml", transfer.UploadOptions{
		SessionID: "s1", TemporarySuffix: ".part",
		OnComplete: func(r transfer.Result) { result = r },
	})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}

	if _, err := upload.Write([]byte("partial")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	upload.TransferError(io.ErrUnexpectedEOF) // simulates a dropped data connection

	if err := upload.Close(); err == nil {
		t.Fatal("Close after TransferError should report the failure")
	}

	if result.Phase != transfer.PhaseFailed {
		t.Errorf("result.Phase = %v, want PhaseFailed", result.Phase)
	}

	if _, exists := fs.files["/archivo.xml"]; exists {
		t.Error("an interrupted transfer must never produce a final file")
	}
}

func TestUploadReadIsUnsupported(t *testing.T) {
	fs := newFakeFS()

	upload, err := transfer.NewUpload(fs, "/archivo.xml", transfer.UploadOptions{SessionID: "s1", TemporarySuffix: ".part"})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}

	defer upload.Close()

	if _, err := upload.Read(make([]byte, 1)); err == nil {
		t.Error("Read on an upload handle should fail")
	}
}

func TestDownloadHappyPath(t *testing.T) {
	fs := newFakeFS()
	fs.files["/archivo.xml"] = []byte("contenido remoto")

	var result transfer.Result

	download, err := transfer.NewDownload(fs, "/archivo.xml", transfer.DownloadOptions{
		SessionID: "s1", VirtualPath: "/archivo.xml", CalculateSHA256: true,
		OnComplete: func(r transfer.Result) { result = r },
	})
	if err != nil {
		t.Fatalf("NewDownload: %v", err)
	}

	data, err := io.ReadAll(download)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if string(data) != "contenido remoto" {
		t.Errorf("read data = %q", data)
	}

	if err := download.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if result.Phase != transfer.PhaseCommitted {
		t.Errorf("result.Phase = %v, want PhaseCommitted", result.Phase)
	}

	if result.Bytes != int64(len("contenido remoto")) {
		t.Errorf("result.Bytes = %d", result.Bytes)
	}
}

func TestDownloadMissingFile(t *testing.T) {
	fs := newFakeFS()

	if _, err := transfer.NewDownload(fs, "/no-existe.xml", transfer.DownloadOptions{}); err == nil {
		t.Fatal("NewDownload should fail for a missing remote file")
	}
}

func TestDownloadWriteIsUnsupported(t *testing.T) {
	fs := newFakeFS()
	fs.files["/a.xml"] = []byte("x")

	download, err := transfer.NewDownload(fs, "/a.xml", transfer.DownloadOptions{})
	if err != nil {
		t.Fatalf("NewDownload: %v", err)
	}

	defer download.Close()

	if _, err := download.Write([]byte("x")); err == nil {
		t.Error("Write on a download handle should fail")
	}
}
