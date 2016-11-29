package lfs

import (
	"fmt"
	"sync"
	"time"

	"github.com/git-lfs/git-lfs/filepathfilter"
	"github.com/rubyist/tracerx"
)

// GitScanner scans objects in a Git repository for LFS pointers.
type GitScanner struct {
	Filter      *filepathfilter.Filter
	callback    GitScannerCallback
	remote      string
	skippedRefs []string

	closed  bool
	started time.Time
	mu      sync.Mutex
}

type GitScannerCallback func(*WrappedPointer, error)

// NewGitScanner initializes a *GitScanner for a Git repository in the current
// working directory.
func NewGitScanner(cb GitScannerCallback) *GitScanner {
	return &GitScanner{started: time.Now(), callback: cb}
}

// Close stops exits once all processing has stopped, and all resources are
// tracked and cleaned up.
func (s *GitScanner) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.closed = true
	tracerx.PerformanceSince("scan", s.started)
}

// RemoteForPush sets up this *GitScanner to scan for objects to push to the
// given remote. Needed for ScanLeftToRemote().
func (s *GitScanner) RemoteForPush(r string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.remote) > 0 && s.remote != r {
		return fmt.Errorf("Trying to set remote to %q, already set to %q", r, s.remote)
	}

	s.remote = r
	s.skippedRefs = calcSkippedRefs(r)
	return nil
}

// ScanLeftToRemote scans through all commits starting at the given ref that the
// given remote does not have. See RemoteForPush().
func (s *GitScanner) ScanLeftToRemote(left string) (*PointerChannelWrapper, error) {
	s.mu.Lock()
	if len(s.remote) == 0 {
		s.mu.Unlock()
		return nil, fmt.Errorf("Unable to scan starting at %q: no remote set.", left)
	}
	s.mu.Unlock()

	return scanRefsToChan(left, "", s.opts(ScanLeftToRemoteMode))
}

// ScanRefRange scans through all commits from the given left and right refs,
// including git objects that have been modified or deleted.
func (s *GitScanner) ScanRefRange(left, right string) (*PointerChannelWrapper, error) {
	opts := s.opts(ScanRefsMode)
	opts.SkipDeletedBlobs = false
	return scanRefsToChan(left, right, opts)
}

// ScanRefWithDeleted scans through all objects in the given ref, including
// git objects that have been modified or deleted.
func (s *GitScanner) ScanRefWithDeleted(ref string) (*PointerChannelWrapper, error) {
	return s.ScanRefRange(ref, "")
}

// ScanRef scans through all objects in the current ref, excluding git objects
// that have been modified or deleted before the ref.
func (s *GitScanner) ScanRef(ref string) (*PointerChannelWrapper, error) {
	opts := s.opts(ScanRefsMode)
	opts.SkipDeletedBlobs = true
	return scanRefsToChan(ref, "", opts)
}

// ScanAll scans through all objects in the git repository.
func (s *GitScanner) ScanAll() (*PointerChannelWrapper, error) {
	opts := s.opts(ScanAllMode)
	opts.SkipDeletedBlobs = false
	return scanRefsToChan("", "", opts)
}

// ScanTree takes a ref and returns WrappedPointer objects in the tree at that
// ref. Differs from ScanRefs in that multiple files in the tree with the same
// content are all reported.
func (s *GitScanner) ScanTree(ref string) (*PointerChannelWrapper, error) {
	return runScanTree(ref)
}

// ScanUnpushed scans history for all LFS pointers which have been added but not
// pushed to the named remote. remote can be left blank to mean 'any remote'.
func (s *GitScanner) ScanUnpushed(remote string) error {
	return scanUnpushed(s.callback, remote)
}

// ScanPreviousVersions scans changes reachable from ref (commit) back to since.
// Returns channel of pointers for *previous* versions that overlap that time.
// Does not include pointers which were still in use at ref (use ScanRefsToChan
// for that)
func (s *GitScanner) ScanPreviousVersions(ref string, since time.Time) (*PointerChannelWrapper, error) {
	return logPreviousSHAs(ref, since)
}

// ScanIndex scans the git index for modified LFS objects.
func (s *GitScanner) ScanIndex(ref string) error {
	return scanIndex(s.callback, ref)
}

func (s *GitScanner) opts(mode ScanningMode) *ScanRefsOptions {
	s.mu.Lock()
	defer s.mu.Unlock()

	opts := newScanRefsOptions()
	opts.ScanMode = mode
	opts.RemoteName = s.remote
	opts.skippedRefs = s.skippedRefs
	return opts
}

type ScanningMode int

const (
	ScanRefsMode         = ScanningMode(iota) // 0 - or default scan mode
	ScanAllMode          = ScanningMode(iota)
	ScanLeftToRemoteMode = ScanningMode(iota)
)

type ScanRefsOptions struct {
	ScanMode         ScanningMode
	RemoteName       string
	SkipDeletedBlobs bool
	skippedRefs      []string
	nameMap          map[string]string
	mutex            *sync.Mutex
}

func (o *ScanRefsOptions) GetName(sha string) (string, bool) {
	o.mutex.Lock()
	name, ok := o.nameMap[sha]
	o.mutex.Unlock()
	return name, ok
}

func (o *ScanRefsOptions) SetName(sha, name string) {
	o.mutex.Lock()
	o.nameMap[sha] = name
	o.mutex.Unlock()
}

func newScanRefsOptions() *ScanRefsOptions {
	return &ScanRefsOptions{
		nameMap: make(map[string]string, 0),
		mutex:   &sync.Mutex{},
	}
}
