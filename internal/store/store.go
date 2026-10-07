// Package store stages units in tmp/ and publishes them into data/ whole.
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

const (
	format     = "lg-store 1"
	rgignore   = "/state/\n/tmp/\n"
	unitPrefix = "unit-"
)

var (
	ErrExists = errors.New("unit already exists")
	ErrFormat = errors.New("unsupported store format")
)

// FS is the filesystem seam that faultfs wraps.
type FS interface {
	Mkdir(path string) error
	Create(path string) (File, error)
	Rename(oldpath, newpath string) error
	SyncDir(path string) error
	RemoveAll(path string) error
	Chmod(path string, mode fs.FileMode) error
	ReadDir(path string) ([]fs.DirEntry, error)
	Lstat(path string) (fs.FileInfo, error)
	Mount(path string) (Mount, error)
}

// Mount identifies the mount holding a path, since rename works only within
// one. ID is the Linux mount id, which tells bind mounts of one device apart,
// and 0 elsewhere.
type Mount struct{ Dev, ID uint64 }

type File interface {
	io.Writer
	Sync() error
	Close() error
}

type Store struct {
	fs                      FS
	data, state, tmp, trash string
}

// Init makes root a store, finishing one that an earlier Init left part
// way. Callers hold state/write.lock. FORMAT is published last, so a store
// with one is complete.
func Init(root string) error { return InitFS(OSFS{}, root) }

// InitFS is Init through fsys.
func InitFS(fsys FS, root string) error {
	if err := check(fsys, root); err != nil {
		return err
	}
	s := newStore(fsys, root)
	for _, dir := range []string{s.data, s.state, s.tmp} {
		if err := mkdirAll(fsys, dir); err != nil {
			return err
		}
	}
	// A writer makes root and state/ before taking write.lock, without fsync.
	for _, dir := range []string{root, filepath.Dir(root)} {
		if err := fsys.SyncDir(dir); err != nil {
			return err
		}
	}
	if err := s.checkDirs(); err != nil {
		return err
	}
	if err := mkdirAll(fsys, s.trash); err != nil {
		return err
	}
	formatFile := filepath.Join(root, "FORMAT")
	if done, err := s.Has(formatFile); done || err != nil {
		return err
	}
	unit, err := s.NewUnit()
	if err != nil {
		return err
	}
	return errors.Join(s.placeFormat(unit, root), unit.Abort())
}

// placeFormat publishes .rgignore, unless the user has one, and then FORMAT.
func (s *Store) placeFormat(unit *Unit, root string) error {
	has, err := s.Has(filepath.Join(root, ".rgignore"))
	if err != nil {
		return err
	}
	if !has {
		if err := unit.place(".rgignore", rgignore, root); err != nil {
			return err
		}
	}
	return unit.place("FORMAT", format+"\n", root)
}

// Check returns an error unless root is an lg-store 1 store, or holds only
// what Init writes before FORMAT.
func Check(root string) error { return check(OSFS{}, root) }

func check(fsys FS, root string) error {
	err := checkFormat(root)
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	entries, err := fsys.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		own, err := isOwn(fsys, root, e.Name())
		if err != nil {
			return err
		}
		if !own {
			// Init publishes FORMAT last, so one that appeared since is a whole store.
			if err := checkFormat(root); !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return fmt.Errorf("%s has no FORMAT and holds files lg did not write; point LG_HOME at an empty or new dir", root)
		}
	}
	return nil
}

// ownState is what lg, or launchd for its daemon.log, writes in state/ before
// the store has FORMAT.
var ownState = []string{
	"write.lock", "daemon.lock", "daemon.pid", "daemon.pid.tmp", "request.lock", "sync-request", "sync-request.tmp",
	"status.json", "status.json.tmp", "lg.db", "lg.db-wal", "lg.db-shm", "lg.db.lock", "daemon.log",
}

// isOwn reports whether name, in a root without FORMAT, is what Init, a
// writer waiting on state/write.lock, a starting daemon, a cycle that could
// not init the store, mkfs or Finder left there, or lg's config when
// LG_CONFIG points there.
func isOwn(fsys FS, root, name string) (bool, error) {
	var ownChild func(string) bool
	switch name {
	case ".rgignore", ".DS_Store", "config.yaml":
		return true, nil
	case "data", "lost+found":
		ownChild = func(string) bool { return false }
	case "state":
		ownChild = func(child string) bool {
			return slices.Contains(ownState, child)
		}
	case "tmp":
		ownChild = func(child string) bool { return isUnit(child) || child == "trash" }
	default:
		return false, nil
	}
	children, err := fsys.ReadDir(filepath.Join(root, name))
	if err != nil {
		return false, err
	}
	for _, child := range children {
		if !ownChild(child.Name()) {
			return false, nil
		}
	}
	return true, nil
}

func Open(root string) (*Store, error) { return OpenFS(OSFS{}, root) }

// OpenFS opens the store at root through fsys. It refuses
// a FORMAT other than lg-store 1, and a tmp/ that cannot be renamed into data/.
func OpenFS(fsys FS, root string) (*Store, error) {
	if err := checkFormat(root); err != nil {
		return nil, err
	}
	s := newStore(fsys, root)
	if err := s.checkDirs(); err != nil {
		return nil, err
	}
	return s, nil
}

// checkDirs refuses a tmp/ that units cannot be renamed from into data/, and
// a symlinked tmp/, tmp/trash/ or data/, since Sweep would empty what they
// point at.
func (s *Store) checkDirs() error {
	for _, dir := range []string{s.data, s.tmp, s.trash} {
		info, err := s.fs.Lstat(dir)
		if dir == s.trash && errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a plain dir; to move the store, move LG_HOME as a whole", dir)
		}
	}
	dataMount, err := s.fs.Mount(s.data)
	if err != nil {
		return err
	}
	tmpMount, err := s.fs.Mount(s.tmp)
	if err != nil {
		return err
	}
	if dataMount != tmpMount {
		return fmt.Errorf("%s and %s are on different devices or mounts, so units cannot be renamed into place; to move the store, move LG_HOME as a whole: %w", s.tmp, s.data, syscall.EXDEV)
	}
	return nil
}

// checkFormat returns ErrFormat unless root's FORMAT is lg-store 1, and
// fs.ErrNotExist when root has no FORMAT.
func checkFormat(root string) error {
	formatFile := filepath.Join(root, "FORMAT")
	got, err := os.ReadFile(formatFile)
	if err != nil {
		return err
	}
	if v := strings.TrimSuffix(string(got), "\n"); v != format {
		return fmt.Errorf("%w: %s is %q; this lg reads %q", ErrFormat, formatFile, v, format)
	}
	return nil
}

func newStore(fsys FS, root string) *Store {
	tmp := filepath.Join(root, "tmp")
	return &Store{fs: fsys, data: filepath.Join(root, "data"), state: filepath.Join(root, "state"), tmp: tmp, trash: filepath.Join(tmp, "trash")}
}

func (s *Store) Data() string { return s.data }

func (s *Store) State() string { return s.state }

// Has reports whether target exists.
func (s *Store) Has(target string) (bool, error) {
	_, err := s.fs.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Names lists the names in dir, and none when dir is missing.
func (s *Store) Names(dir string) ([]string, error) {
	entries, err := s.fs.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}

// FindRunDir gives runDir when it exists, else the dir beside it that holds
// the same run, named <id>_<slugs> with slugs that may differ, else runDir.
func (s *Store) FindRunDir(runDir string) (string, error) {
	exists, err := s.Has(runDir)
	if err != nil {
		return "", err
	}
	if exists {
		return runDir, nil
	}
	date, name := filepath.Split(runDir)
	id, _, _ := strings.Cut(name, "_")
	entries, err := s.fs.ReadDir(date)
	if errors.Is(err, fs.ErrNotExist) {
		return runDir, nil
	}
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id+"_") {
			return filepath.Join(date, e.Name()), nil
		}
	}
	return runDir, nil
}

// Sweep empties tmp/ and tmp/trash/ of what dead writers left, and keeps
// tmp/trash/ so that Evict needs no mkdir. Callers hold state/write.lock, so
// no live writer uses them.
func (s *Store) Sweep() error {
	if err := s.removeChildren(s.tmp, s.trash); err != nil {
		return err
	}
	return s.removeChildren(s.trash, "")
}

func (s *Store) removeChildren(dir, except string) error {
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if child := filepath.Join(dir, e.Name()); child != except {
			if err := removeTree(s.fs, child); err != nil {
				return err
			}
		}
	}
	return nil
}

// Unit is a directory staged under tmp/ until Publish renames it into data/.
type Unit struct {
	fs       FS
	dir      string
	dirs     []string // created under dir, parents first
	unclosed map[string]bool
	sums     map[string]Sum
}

func (s *Store) NewUnit() (*Unit, error) {
	dir := filepath.Join(s.tmp, unitPrefix+randomName())
	if err := s.fs.Mkdir(dir); err != nil {
		return nil, err
	}
	return &Unit{fs: s.fs, dir: dir, unclosed: map[string]bool{}, sums: map[string]Sum{}}, nil
}

func randomName() string { return strconv.FormatUint(rand.Uint64(), 36) }

func isUnit(name string) bool { return strings.HasPrefix(name, unitPrefix) }

// Publish makes the unit durable, then renames it to target, which must not exist.
func (s *Store) Publish(u *Unit, target string) error {
	if len(u.unclosed) > 0 {
		return fmt.Errorf("member %q is still open", slices.Sorted(maps.Keys(u.unclosed))[0])
	}
	for _, dir := range slices.Backward(append([]string{u.dir}, u.dirs...)) {
		if err := s.fs.SyncDir(dir); err != nil {
			return err
		}
	}
	parent := filepath.Dir(target)
	if err := s.refuseLinks(parent); err != nil {
		return err
	}
	if err := mkdirAll(s.fs, parent); err != nil {
		return err
	}
	if err := s.fs.Rename(u.dir, target); err != nil {
		if errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.ENOTEMPTY) {
			return fmt.Errorf("%w: %s", ErrExists, target)
		}
		return err
	}
	return s.fs.SyncDir(parent)
}

// refuseLinks refuses a symlink at dir or at a parent of it below data/,
// since retention does not follow one.
func (s *Store) refuseLinks(dir string) error {
	for ; within(dir, s.data); dir = filepath.Dir(dir) {
		info, err := s.fs.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink, which lg does not follow below %s", dir, s.data)
		}
	}
	return nil
}

func within(path, dir string) bool { return strings.HasPrefix(path, dir+string(filepath.Separator)) }

func mkdirAll(fsys FS, dir string) error {
	_, err := mkdirBelow(fsys, "", dir, true)
	return err
}

// mkdirBelow makes path and any missing parents below base, returning those
// it made, parents first. With syncParents, it fsyncs each new dir's parent.
// It never makes base, so it fails once base is gone.
func mkdirBelow(fsys FS, base, path string, syncParents bool) ([]string, error) {
	var missing []string
	for dir := path; dir != base; dir = filepath.Dir(dir) {
		_, err := fsys.Lstat(dir)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, dir)
	}
	slices.Reverse(missing)
	for _, dir := range missing {
		// Another lg process may make state/ before it holds write.lock.
		if err := fsys.Mkdir(dir); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if syncParents {
			if err := fsys.SyncDir(filepath.Dir(dir)); err != nil {
				return nil, err
			}
		}
	}
	return missing, nil
}

// place writes name in the unit, then renames it into dir, which must not
// have one, so a reader sees no file or the whole of it.
func (u *Unit) place(name, content, dir string) error {
	w, err := u.Create(name, Unlimited)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, content); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := u.fs.Rename(filepath.Join(u.dir, name), filepath.Join(dir, name)); err != nil {
		return err
	}
	return u.fs.SyncDir(dir)
}

// Sum is the size and SHA-256 of a member's bytes.
type Sum struct {
	Bytes  int64
	SHA256 string
}

func (u *Unit) Sum(name string) (Sum, error) {
	sum, closed := u.sums[name]
	if !closed {
		return Sum{}, fmt.Errorf("member %q is not closed", name)
	}
	return sum, nil
}

// Open opens a closed member for reading.
func (u *Unit) Open(name string) (*os.File, error) {
	if _, closed := u.sums[name]; !closed {
		return nil, fmt.Errorf("member %q is not closed", name)
	}
	return os.Open(filepath.Join(u.dir, name))
}

// Remove deletes a closed member.
func (u *Unit) Remove(name string) error {
	if _, closed := u.sums[name]; !closed {
		return fmt.Errorf("member %q is not closed", name)
	}
	delete(u.sums, name)
	return u.fs.RemoveAll(filepath.Join(u.dir, name))
}

// Abort removes the staged unit.
func (u *Unit) Abort() error { return removeTree(u.fs, u.dir) }

// removeTree removes dir, which is under tmp/, also when a dir in it lacks
// owner write permission, as one from an extracted archive may. RemoveAll
// does not always report that as ErrPermission, so any error gets a retry.
func removeTree(fsys FS, dir string) error {
	if fsys.RemoveAll(dir) == nil {
		return nil
	}
	if err := makeWritable(fsys, dir); err != nil {
		return err
	}
	return fsys.RemoveAll(dir)
}

// makeWritable gives the owner rwx permission on dir and each dir in it.
func makeWritable(fsys FS, dir string) error {
	info, err := fsys.Lstat(dir)
	if err != nil || !info.IsDir() {
		return err
	}
	if err := fsys.Chmod(dir, info.Mode().Perm()|0o700); err != nil {
		return err
	}
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := makeWritable(fsys, filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Unlimited lets a member grow to any size.
const Unlimited int64 = math.MaxInt64

// ErrTooLarge is a write past a member's maxBytes.
var ErrTooLarge = errors.New("member too large")

// Create opens a new member file, creating its parent dirs within the unit.
// It fails once the unit's dir is gone. Closing it fsyncs it.
func (u *Unit) Create(name string, maxBytes int64) (io.WriteCloser, error) {
	if !filepath.IsLocal(name) || slices.Contains(strings.Split(filepath.ToSlash(name), "/"), "..") {
		return nil, fmt.Errorf("member name %q is absolute or contains \"..\"", name)
	}
	path := filepath.Join(u.dir, name)
	made, err := mkdirBelow(u.fs, u.dir, filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	u.dirs = append(u.dirs, made...)
	f, err := u.fs.Create(path)
	if err != nil {
		return nil, err
	}
	u.unclosed[name] = true
	return &member{File: f, unit: u, name: name, maxBytes: maxBytes, hash: sha256.New()}, nil
}

// member sums its bytes, refuses more than maxBytes, and fsyncs its file on
// Close, which Publish requires first.
type member struct {
	File
	unit     *Unit
	name     string
	maxBytes int64
	hash     hash.Hash
	bytes    int64
}

// Write writes none of p when p would take the member past maxBytes.
func (m *member) Write(p []byte) (int, error) {
	if int64(len(p)) > m.maxBytes-m.bytes {
		return 0, fmt.Errorf("%w: %q exceeds %d bytes", ErrTooLarge, m.name, m.maxBytes)
	}
	n, err := m.File.Write(p)
	m.hash.Write(p[:n])
	m.bytes += int64(n)
	return n, err
}

func (m *member) Close() error {
	delete(m.unit.unclosed, m.name)
	if err := m.Sync(); err != nil {
		_ = m.File.Close()
		return err
	}
	if err := m.File.Close(); err != nil {
		return err
	}
	m.unit.sums[m.name] = Sum{Bytes: m.bytes, SHA256: hex.EncodeToString(m.hash.Sum(nil))}
	return nil
}

// WriteJSON stores raw indented two spaces, plus a newline, so each key sits
// on its own line for grep.
func (u *Unit) WriteJSON(name string, raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	w, err := u.Create(name, Unlimited)
	if err != nil {
		return err
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// WriteValue stores v as WriteJSON does, with <, > and & as they are, so rg finds them.
func (u *Unit) WriteValue(name string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return u.WriteJSON(name, buf.Bytes())
}

// ReplaceFile replaces path with data, so that a crash leaves the old file
// or the new one. Callers hold state/write.lock.
func (s *Store) ReplaceFile(path string, data []byte) error { return ReplaceFileFS(s.fs, path, data) }

// ReplaceFileFS is ReplaceFile through fsys, for callers with no open Store.
func ReplaceFileFS(fsys FS, path string, data []byte) error {
	tmp := path + ".tmp"
	if err := fsys.RemoveAll(tmp); err != nil {
		return err
	}
	f, err := fsys.Create(tmp)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = fsys.Rename(tmp, path)
	}
	if err != nil {
		_ = fsys.RemoveAll(tmp)
		return err
	}
	return fsys.SyncDir(filepath.Dir(path))
}

// Rename renames oldpath to newpath and makes it durable. Callers hold
// state/write.lock.
func (s *Store) Rename(oldpath, newpath string) error {
	if err := s.fs.Rename(oldpath, newpath); err != nil {
		return err
	}
	return s.fs.SyncDir(filepath.Dir(newpath))
}

// RemoveEmpty removes dir when it holds nothing but Finder's .DS_Store.
// Callers hold state/write.lock.
func (s *Store) RemoveEmpty(dir string) error {
	entries, err := s.fs.ReadDir(dir)
	if err != nil || slices.ContainsFunc(entries, func(e fs.DirEntry) bool { return e.Name() != ".DS_Store" }) {
		return err
	}
	return s.fs.RemoveAll(dir)
}

// Evict moves dir into tmp/trash/ with one rename, so a reader sees all of
// it or none, and then deletes it. It makes nothing, so it works on a full
// disk. Callers hold state/write.lock.
func (s *Store) Evict(dir string) error {
	trashed := filepath.Join(s.trash, randomName())
	if err := s.Rename(dir, trashed); err != nil {
		return err
	}
	return removeTree(s.fs, trashed)
}
