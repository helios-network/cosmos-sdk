package baseapp

import (
	"sync/atomic"
	"testing"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	"cosmossdk.io/store/archivekv"
	storemetrics "cosmossdk.io/store/metrics"
	dbm "github.com/cosmos/cosmos-db"
)

func TestPrunerStop(t *testing.T) {
	// Créer une DB temporaire pour le test
	db, err := dbm.NewDB("test", dbm.MemDBBackend, "")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer db.Close()

	logger := log.NewNopLogger()
	cms := store.NewCommitMultiStore(db, logger, storemetrics.NewNoOpMetrics())
	acms := make(map[string]*archivekv.Store)

	// Créer le pruner
	pruner := NewPruner(cms, acms, logger)

	// Vérifier que le pruner est actif
	if atomic.LoadInt32(&pruner.stopped) != 0 {
		t.Error("pruner should not be stopped initially")
	}

	// Attendre un peu pour s'assurer que la goroutine worker est démarrée
	time.Sleep(10 * time.Millisecond)

	// Arrêter le pruner
	start := time.Now()
	pruner.Stop()
	stopTime := time.Since(start)

	// Vérifier que l'arrêt s'est fait rapidement (moins de 1 seconde)
	if stopTime > time.Second {
		t.Errorf("pruner stop took too long: %v", stopTime)
	}

	// Vérifier que le pruner est marqué comme arrêté
	if atomic.LoadInt32(&pruner.stopped) != 1 {
		t.Error("pruner should be marked as stopped")
	}

	// Tester qu'un deuxième appel à Stop() ne fait rien
	pruner.Stop() // Ne devrait rien faire
}

func TestPrunerMultipleStop(t *testing.T) {
	db, err := dbm.NewDB("test", dbm.MemDBBackend, "")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer db.Close()

	logger := log.NewNopLogger()
	cms := store.NewCommitMultiStore(db, logger, storemetrics.NewNoOpMetrics())
	acms := make(map[string]*archivekv.Store)
	pruner := NewPruner(cms, acms, logger)

	// Appeler Stop() plusieurs fois
	for i := 0; i < 5; i++ {
		pruner.Stop()
	}

	// Vérifier que le pruner est toujours marqué comme arrêté
	if atomic.LoadInt32(&pruner.stopped) != 1 {
		t.Error("pruner should be marked as stopped after multiple Stop calls")
	}
}

func TestPrunerEnqueue(t *testing.T) {
	db, err := dbm.NewDB("test", dbm.MemDBBackend, "")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer db.Close()

	logger := log.NewNopLogger()
	cms := store.NewCommitMultiStore(db, logger, storemetrics.NewNoOpMetrics())
	acms := make(map[string]*archivekv.Store)
	pruner := NewPruner(cms, acms, logger)

	// Attendre un peu pour s'assurer que les goroutines sont démarrées
	time.Sleep(10 * time.Millisecond)

	// Tester l'enqueue d'une tâche
	task := pruneTask{
		retainHeight:  100,
		currentHeight: 200,
		label:         "test_task",
	}

	success := pruner.TryEnqueue(task)
	if !success {
		t.Error("failed to enqueue pruning task")
	}

	// Arrêter le pruner
	pruner.Stop()
}
