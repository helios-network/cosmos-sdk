package baseapp

import (
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/store/archivekv"
	storetypes "cosmossdk.io/store/types"
)

type pruneTask struct {
	retainHeight  int64
	currentHeight int64
	label         string
}

type Pruner struct {
	cms     storetypes.CommitMultiStore
	acms    map[string]*archivekv.Store
	logger  log.Logger
	wg      sync.WaitGroup // pour attendre que worker se termine
	stopped int32          // atomic flag pour éviter les appels multiples à Stop
	stopCh  chan struct{}  // canal pour arrêter le worker
	queue   chan pruneTask // taille 1 pour éviter le flood
}

func NewPruner(cms storetypes.CommitMultiStore, acms map[string]*archivekv.Store, logger log.Logger) *Pruner {
	p := &Pruner{
		cms:    cms,
		acms:   acms,
		logger: logger,
		stopCh: make(chan struct{}),
		queue:  make(chan pruneTask, 1),
	}

	// Démarrer le worker
	p.wg.Add(1)
	go p.worker()

	// Démarrer la gestion des signaux
	go p.handleSignals()

	return p
}

func (p *Pruner) handleSignals() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)

	<-sigCh
	p.logger.Info("received shutdown signal, stopping pruner")

	// Éviter les fermetures multiples du canal
	if atomic.CompareAndSwapInt32(&p.stopped, 0, 1) {
		// Fermer le canal d'arrêt
		close(p.stopCh)

		// Fermer le canal de queue
		close(p.queue)

		// Attendre que le worker se termine
		p.wg.Wait()

		p.logger.Info("pruner stopped gracefully")
	}
}

func (p *Pruner) worker() {
	defer p.wg.Done()
	for {
		select {
		case task, ok := <-p.queue:
			if !ok {
				// Canal fermé, on sort
				return
			}
			start := time.Now()
			p.logger.Info("pruning_start", "label", task.label, "retainHeight", task.retainHeight, "currentHeight", task.currentHeight)

			if err := p.performPruning(task.retainHeight, task.currentHeight); err != nil {
				p.logger.Error("pruning_error", "label", task.label, "err", err)
			} else {
				p.logger.Info("pruning_done", "label", task.label, "ms", time.Since(start).Milliseconds())
			}
		case <-p.stopCh:
			// Signal d'arrêt reçu, on sort
			return
		}
	}
}

func (p *Pruner) performPruning(retainHeight int64, currentHeight int64) error {
	// Prune main store
	if err := p.cms.DeleteFromBaseVersionTo(retainHeight); err != nil {
		p.logger.Error("failed to prune main store", "error", err)
		return err
	}

	// Prune archive stores with error handling
	for name, acm := range p.acms {
		if err := acm.DeleteFromBaseVersionTo(uint64(retainHeight)); err != nil {
			p.logger.Error("failed to prune archive store", "store", name, "error", err)
			// Continue with other stores even if one fails
		}
	}

	return nil
}

// Non-bloquant : si worker occupé ou queue pleine, on skippe
func (p *Pruner) TryEnqueue(task pruneTask) bool {
	select {
	case p.queue <- task:
		return true
	default:
		return false
	}
}

func (p *Pruner) Stop() {
	// Éviter les appels multiples à Stop
	if !atomic.CompareAndSwapInt32(&p.stopped, 0, 1) {
		return
	}

	p.logger.Info("Stopping pruner manually")

	// Fermer le canal d'arrêt pour arrêter le worker
	close(p.stopCh)

	// Fermer le canal de queue
	close(p.queue)

	// Attendre que worker se termine avec un timeout
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("Pruner stopped gracefully")
	case <-time.After(5 * time.Second):
		p.logger.Warn("Pruner stop timeout - forcing shutdown")
	}
}
