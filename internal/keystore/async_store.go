package keystore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const asyncStatusRetryDelay = 250 * time.Millisecond

type AsyncStatusStoreOptions struct {
	SetStatusTimeout time.Duration
	ErrorHandler     func(error)
}

type conditionalStatusStoreWithContext interface {
	ConditionalStatusStore
	SetStatusIfVersionContext(ctx context.Context, vendor, key string, expectedVersion int64, status, reason, actor string) error
}

type pendingStatusUpdate struct {
	id              uint64
	vendor          string
	key             string
	expectedVersion int64
	status          string
	reason          string
	actor           string
	at              time.Time
}

type AsyncStatusStore struct {
	base        Store
	conditional ConditionalStatusStore
	ctxAware    conditionalStatusStoreWithContext
	timeout     time.Duration
	onError     func(error)

	mu      sync.RWMutex
	pending map[string]pendingStatusUpdate
	nextID  uint64
	closed  bool

	wakeCh chan struct{}
	stopCh chan struct{}
	doneCh chan error
}

func NewAsyncStatusStore(base Store, opts AsyncStatusStoreOptions) (*AsyncStatusStore, error) {
	if base == nil {
		return nil, errors.New("base store is nil")
	}
	conditional, ok := base.(ConditionalStatusStore)
	if !ok {
		return nil, errors.New("base store does not support conditional status updates")
	}

	timeout := opts.SetStatusTimeout
	if timeout <= 0 {
		timeout = time.Second
	}
	onError := opts.ErrorHandler
	if onError == nil {
		onError = func(err error) {
			log.Printf("async upstream key status sync failed: %v", err)
		}
	}

	store := &AsyncStatusStore{
		base:        base,
		conditional: conditional,
		timeout:     timeout,
		onError:     onError,
		pending:     make(map[string]pendingStatusUpdate),
		wakeCh:      make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan error, 1),
	}
	if ctxAware, ok := conditional.(conditionalStatusStoreWithContext); ok {
		store.ctxAware = ctxAware
	}
	go store.run()
	return store, nil
}

func (s *AsyncStatusStore) Info() Info {
	return s.base.Info()
}

func (s *AsyncStatusStore) ListAll() (map[string][]Record, error) {
	all, err := s.base.ListAll()
	if err != nil {
		return nil, err
	}
	for _, update := range s.pendingSnapshot() {
		applyPendingRecord(all, update)
	}
	return all, nil
}

func (s *AsyncStatusStore) List(vendor string) ([]Record, error) {
	records, err := s.base.List(vendor)
	if err != nil {
		return nil, err
	}
	out := append([]Record(nil), records...)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, update := range s.pending {
		if update.vendor != normalizeVendor(vendor) {
			continue
		}
		out = applyPendingVendorRecord(out, update)
	}
	sortRecords(out)
	return out, nil
}

func (s *AsyncStatusStore) KeyMap() (map[string][]string, error) {
	return s.base.KeyMap()
}

func (s *AsyncStatusStore) Replace(vendor string, keys []string) error {
	if err := s.base.Replace(vendor, keys); err != nil {
		return err
	}
	s.clearVendorPending(vendor)
	return nil
}

func (s *AsyncStatusStore) Append(vendor string, keys []string) (int, error) {
	added, err := s.base.Append(vendor, keys)
	if err != nil {
		return 0, err
	}
	for _, key := range NormalizeKeys(keys) {
		s.clearKeyPending(vendor, key)
	}
	return added, nil
}

func (s *AsyncStatusStore) Delete(vendor string, keys []string) (int, error) {
	removed, err := s.base.Delete(vendor, keys)
	if err != nil {
		return 0, err
	}
	for _, key := range NormalizeKeys(keys) {
		s.clearKeyPending(vendor, key)
	}
	return removed, nil
}

func (s *AsyncStatusStore) SetStatus(vendor, key, status, reason, actor string) error {
	if err := s.base.SetStatus(vendor, key, status, reason, actor); err != nil {
		return err
	}
	s.clearKeyPending(vendor, key)
	return nil
}

func (s *AsyncStatusStore) SetStatusIfVersion(vendor, key string, expectedVersion int64, status, reason, actor string) error {
	update, err := newPendingStatusUpdate(vendor, key, expectedVersion, status, reason, actor)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("async status store is closed")
	}
	s.nextID++
	update.id = s.nextID
	s.pending[pendingStatusKey(update.vendor, update.key)] = update
	s.mu.Unlock()

	s.signalFlush()
	return nil
}

func (s *AsyncStatusStore) DeleteVendor(vendor string) error {
	if err := s.base.DeleteVendor(vendor); err != nil {
		return err
	}
	s.clearVendorPending(vendor)
	return nil
}

func (s *AsyncStatusStore) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.stopCh)
	s.mu.Unlock()

	flushErr := <-s.doneCh
	closeErr := s.base.Close()
	return errors.Join(flushErr, closeErr)
}

func (s *AsyncStatusStore) run() {
	var (
		retryTimer *time.Timer
		retryCh    <-chan time.Time
	)
	stopTimer := func() {
		if retryTimer == nil {
			return
		}
		if !retryTimer.Stop() {
			select {
			case <-retryTimer.C:
			default:
			}
		}
		retryTimer = nil
		retryCh = nil
	}

	var finalErr error
	defer func() {
		stopTimer()
		s.doneCh <- finalErr
		close(s.doneCh)
	}()

	for {
		select {
		case <-s.wakeCh:
			if s.flushPending() {
				stopTimer()
				continue
			}
			if retryTimer == nil {
				retryTimer = time.NewTimer(asyncStatusRetryDelay)
				retryCh = retryTimer.C
			} else {
				retryTimer.Reset(asyncStatusRetryDelay)
			}
		case <-retryCh:
			retryTimer = nil
			retryCh = nil
			if s.flushPending() {
				continue
			}
			retryTimer = time.NewTimer(asyncStatusRetryDelay)
			retryCh = retryTimer.C
		case <-s.stopCh:
			finalErr = s.flushPendingFinal()
			return
		}
	}
}

func (s *AsyncStatusStore) flushPending() bool {
	updates := s.pendingSnapshot()
	if len(updates) == 0 {
		return true
	}

	allSynced := true
	for _, update := range updates {
		err := s.applyPending(update)
		switch {
		case err == nil, errors.Is(err, ErrVersionMismatch), errors.Is(err, ErrKeyNotFound):
			s.deletePendingIfMatch(update)
		default:
			allSynced = false
			s.onError(fmt.Errorf("vendor=%s key=%s: %w", update.vendor, update.key, err))
		}
	}
	return allSynced
}

func (s *AsyncStatusStore) flushPendingFinal() error {
	updates := s.pendingSnapshot()
	if len(updates) == 0 {
		return nil
	}

	var errs []error
	for _, update := range updates {
		err := s.applyPending(update)
		switch {
		case err == nil, errors.Is(err, ErrVersionMismatch), errors.Is(err, ErrKeyNotFound):
			s.deletePendingIfMatch(update)
		default:
			errs = append(errs, fmt.Errorf("vendor=%s key=%s: %w", update.vendor, update.key, err))
		}
	}
	return errors.Join(errs...)
}

func (s *AsyncStatusStore) applyPending(update pendingStatusUpdate) error {
	if s.ctxAware != nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
		defer cancel()
		return s.ctxAware.SetStatusIfVersionContext(ctx, update.vendor, update.key, update.expectedVersion, update.status, update.reason, update.actor)
	}
	return s.conditional.SetStatusIfVersion(update.vendor, update.key, update.expectedVersion, update.status, update.reason, update.actor)
}

func (s *AsyncStatusStore) pendingSnapshot() []pendingStatusUpdate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]pendingStatusUpdate, 0, len(s.pending))
	for _, update := range s.pending {
		out = append(out, update)
	}
	return out
}

func (s *AsyncStatusStore) deletePendingIfMatch(update pendingStatusUpdate) {
	key := pendingStatusKey(update.vendor, update.key)
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.pending[key]
	if !ok || current.id != update.id {
		return
	}
	delete(s.pending, key)
}

func (s *AsyncStatusStore) clearVendorPending(vendor string) {
	vendor = normalizeVendor(vendor)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, update := range s.pending {
		if update.vendor == vendor {
			delete(s.pending, key)
		}
	}
}

func (s *AsyncStatusStore) clearKeyPending(vendor, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, pendingStatusKey(normalizeVendor(vendor), strings.TrimSpace(key)))
}

func (s *AsyncStatusStore) signalFlush() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

func newPendingStatusUpdate(vendor, key string, expectedVersion int64, status, reason, actor string) (pendingStatusUpdate, error) {
	vendor = normalizeVendor(vendor)
	key = strings.TrimSpace(key)
	if vendor == "" {
		return pendingStatusUpdate{}, errors.New("vendor is required")
	}
	if key == "" {
		return pendingStatusUpdate{}, errors.New("key is required")
	}
	return pendingStatusUpdate{
		vendor:          vendor,
		key:             key,
		expectedVersion: expectedVersion,
		status:          NormalizeStatus(status),
		reason:          strings.TrimSpace(reason),
		actor:           strings.TrimSpace(actor),
		at:              time.Now().UTC(),
	}, nil
}

func pendingStatusKey(vendor, key string) string {
	return vendor + "\x00" + key
}

func applyPendingRecord(all map[string][]Record, update pendingStatusUpdate) {
	records := applyPendingVendorRecord(all[update.vendor], update)
	if len(records) == 0 {
		delete(all, update.vendor)
		return
	}
	all[update.vendor] = records
}

func applyPendingVendorRecord(records []Record, update pendingStatusUpdate) []Record {
	next := append([]Record(nil), records...)
	for i := range next {
		if next[i].Key != update.key {
			continue
		}
		next[i].Status = update.status
		next[i].Version = max(next[i].Version, update.expectedVersion+1)
		next[i].UpdatedAt = update.at
		if update.status == KeyStatusActive {
			next[i].DisableReason = ""
			next[i].DisabledAt = nil
			next[i].DisabledBy = ""
		} else {
			disabledAt := update.at
			next[i].DisableReason = update.reason
			next[i].DisabledAt = &disabledAt
			next[i].DisabledBy = update.actor
		}
		next[i] = NormalizeRecord(next[i])
		sortRecords(next)
		return next
	}

	if update.status == KeyStatusActive {
		sortRecords(next)
		return next
	}
	disabledAt := update.at
	next = append(next, NormalizeRecord(Record{
		Key:           update.key,
		Status:        update.status,
		DisableReason: update.reason,
		DisabledAt:    &disabledAt,
		DisabledBy:    update.actor,
		Version:       max(int64(1), update.expectedVersion+1),
		CreatedAt:     update.at,
		UpdatedAt:     update.at,
	}))
	sortRecords(next)
	return next
}
