package baseapp

import (
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
)

type compactTask struct {
	start, limit []byte // nil,nil = full range
	label        string
}

type Compactor struct {
	db       dbm.DB
	inFlight int32            // atomic
	queue    chan compactTask // taille 1 pour éviter le flood
	logger   log.Logger
	wg       sync.WaitGroup // pour attendre que worker se termine
	stopped  int32          // atomic flag pour éviter les appels multiples à Stop
	stopCh   chan struct{}  // canal pour arrêter le worker
}

func NewCompactor(db dbm.DB, logger log.Logger) *Compactor {
	c := &Compactor{
		db:     db,
		queue:  make(chan compactTask, 1),
		logger: logger,
		stopCh: make(chan struct{}),
	}

	// Démarrer le worker
	c.wg.Add(1)
	go c.worker()

	// Démarrer la gestion des signaux
	go c.handleSignals()

	return c
}

func (c *Compactor) handleSignals() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)

	<-sigCh
	c.logger.Info("received shutdown signal, stopping compactor")

	// Éviter les fermetures multiples du canal
	if atomic.CompareAndSwapInt32(&c.stopped, 0, 1) {
		// Fermer le canal d'arrêt
		close(c.stopCh)

		// Fermer le canal de queue
		close(c.queue)

		// Attendre que le worker se termine
		c.wg.Wait()

		c.logger.Info("compactor stopped gracefully")
	}
}

func (c *Compactor) worker() {
	defer c.wg.Done()
	for {
		select {
		case task, ok := <-c.queue:
			if !ok {
				// Canal fermé, on sort
				return
			}
			if !atomic.CompareAndSwapInt32(&c.inFlight, 0, 1) {
				continue
			}
			start := time.Now()
			c.logger.Info("compaction_start", "label", task.label)
			if err := forceCompact(c.db, task.start, task.limit); err != nil {
				c.logger.Error("compaction_error", "label", task.label, "err", err)
			} else {
				c.logger.Info("compaction_done", "label", task.label, "ms", time.Since(start).Milliseconds())
			}
			atomic.StoreInt32(&c.inFlight, 0)
		case <-c.stopCh:
			// Signal d'arrêt reçu, on sort
			return
		}
	}
}

// Non-bloquant : si worker occupé ou queue pleine, on skippe
func (c *Compactor) TryEnqueue(task compactTask) bool {
	if atomic.LoadInt32(&c.inFlight) == 1 {
		return false
	}
	select {
	case c.queue <- task:
		return true
	default:
		return false
	}
}

func (c *Compactor) Stop() {
	// Éviter les appels multiples à Stop
	if !atomic.CompareAndSwapInt32(&c.stopped, 0, 1) {
		return
	}

	c.logger.Info("Stopping compactor manually")

	// Fermer le canal d'arrêt pour arrêter le worker
	close(c.stopCh)

	// Fermer le canal de queue
	close(c.queue)

	// Attendre que worker se termine avec un timeout
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.logger.Info("Compactor stopped gracefully")
	case <-time.After(5 * time.Second):
		c.logger.Warn("Compactor stop timeout - forcing shutdown")
	}
}
