package ingest_test

import (
	"context"
	"maps"
	"testing"

	"quisac/internal/core"
	"quisac/internal/ingest"
)

// fakeStore records surgical calls and tracks head/manifest so ProcessPush's
// decisions can be asserted without Neo4j.
type fakeStore struct {
	head     string
	manifest map[string]bool
	raw      map[string][]string

	saved       int
	replaced    []string
	deleted     []string
	setImports  map[string][]string
	rebuilt     bool
	headUpdates []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{manifest: map[string]bool{}, raw: map[string][]string{}, setImports: map[string][]string{}}
}

func (f *fakeStore) SaveGraph(_ context.Context, _, headSHA string, _ *core.Graph) error {
	f.saved++
	f.head = headSHA
	return nil
}
func (f *fakeStore) ReplaceFile(_ context.Context, _, path string, _ *core.Graph) error {
	f.replaced = append(f.replaced, path)
	f.manifest[path] = true
	return nil
}
func (f *fakeStore) DeleteFile(_ context.Context, _, path string) error {
	f.deleted = append(f.deleted, path)
	delete(f.manifest, path)
	return nil
}
func (f *fakeStore) SetImports(_ context.Context, _, from string, targets []string) error {
	f.setImports[from] = targets
	return nil
}
func (f *fakeStore) RebuildImports(_ context.Context, _ string, _ [][2]string) error {
	f.rebuilt = true
	return nil
}
func (f *fakeStore) FileManifest(_ context.Context, _ string) (map[string]bool, error) {
	m := map[string]bool{}
	maps.Copy(m, f.manifest)
	return m, nil
}
func (f *fakeStore) RawImports(_ context.Context, _ string) (map[string][]string, error) {
	return f.raw, nil
}
func (f *fakeStore) GetHead(_ context.Context, _ string) (string, error) { return f.head, nil }
func (f *fakeStore) SetHead(_ context.Context, _, headSHA string) error {
	f.headUpdates = append(f.headUpdates, headSHA)
	f.head = headSHA
	return nil
}

// fakeSource serves file contents from an in-memory map.
type fakeSource struct {
	files      map[string]string
	downloaded int
}

func (s *fakeSource) DownloadRepo(context.Context, string, string) (string, func(), error) {
	s.downloaded++
	return "", func() {}, errFakeDownload // FullIndex path isn't exercised for the incremental cases
}
func (s *fakeSource) GetFileContent(_ context.Context, _, _, path, _ string) ([]byte, bool, error) {
	c, ok := s.files[path]
	if !ok {
		return nil, false, nil
	}
	return []byte(c), true, nil
}
func (s *fakeSource) HeadSHA(context.Context, string, string) (string, error) { return "", nil }

var errFakeDownload = errFake("download called")

type errFake string

func (e errFake) Error() string { return string(e) }

func TestProcessPush_ModifyOnlyIsSurgical(t *testing.T) {
	store := newFakeStore()
	store.head = "sha1"
	store.manifest["main.py"] = true
	store.manifest["utils.py"] = true

	src := &fakeSource{files: map[string]string{
		"main.py": "from utils import helper\n\ndef main():\n    helper()\n",
	}}

	ix := ingest.New(src, store, nil)
	err := ix.ProcessPush(context.Background(), "1", "o/r", "sha1", "sha2",
		[]string{"main.py"}, true)
	if err != nil {
		t.Fatalf("ProcessPush: %v", err)
	}

	if src.downloaded != 0 {
		t.Error("modify-only push should not download the repo")
	}
	if len(store.replaced) != 1 || store.replaced[0] != "main.py" {
		t.Errorf("expected ReplaceFile(main.py), got %v", store.replaced)
	}
	if store.rebuilt {
		t.Error("modify-only push should not rebuild all imports")
	}
	if got := store.setImports["main.py"]; len(got) != 1 || got[0] != "utils.py" {
		t.Errorf("expected outbound import main.py→utils.py, got %v", got)
	}
	if store.head != "sha2" {
		t.Errorf("head not advanced: %q", store.head)
	}
}

func TestProcessPush_AddedFileIsStructural(t *testing.T) {
	store := newFakeStore()
	store.head = "sha1"
	store.manifest["main.py"] = true
	store.raw = map[string][]string{"main.py": {"from utils import helper"}}

	src := &fakeSource{files: map[string]string{
		"utils.py": "def helper():\n    pass\n",
	}}

	ix := ingest.New(src, store, nil)
	if err := ix.ProcessPush(context.Background(), "1", "o/r", "sha1", "sha2",
		[]string{"utils.py"}, true); err != nil {
		t.Fatalf("ProcessPush: %v", err)
	}

	if !store.rebuilt {
		t.Error("adding a file should trigger a full import rebuild (inbound edges)")
	}
	if store.head != "sha2" {
		t.Errorf("head not advanced: %q", store.head)
	}
}

func TestProcessPush_DriftFallsBackToFullIndex(t *testing.T) {
	store := newFakeStore()
	store.head = "sha1"
	src := &fakeSource{files: map[string]string{}}

	ix := ingest.New(src, store, nil)
	// before != stored head → drift → FullIndex → DownloadRepo (our fake errors).
	err := ix.ProcessPush(context.Background(), "1", "o/r", "STALE", "sha2", []string{"x.py"}, true)
	if err == nil {
		t.Fatal("expected FullIndex path (download) to be taken on drift")
	}
	if src.downloaded != 1 {
		t.Errorf("drift should trigger one download, got %d", src.downloaded)
	}
}

func TestProcessPush_NoIndexableChangesJustBumpsHead(t *testing.T) {
	store := newFakeStore()
	store.head = "sha1"
	src := &fakeSource{files: map[string]string{}}

	ix := ingest.New(src, store, nil)
	if err := ix.ProcessPush(context.Background(), "1", "o/r", "sha1", "sha2",
		[]string{"README.md", "docs/guide.md"}, true); err != nil {
		t.Fatalf("ProcessPush: %v", err)
	}
	if len(store.replaced) != 0 || len(store.deleted) != 0 {
		t.Error("doc-only push should touch no files")
	}
	if store.head != "sha2" {
		t.Errorf("head should still advance, got %q", store.head)
	}
}
