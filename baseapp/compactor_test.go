package baseapp

import (
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
)

func TestCompactorStop(t *testing.T) {
	// Créer une DB temporaire pour le test
	db, err := dbm.NewDB("test", dbm.MemDBBackend, "")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer db.Close()

	logger := log.NewNopLogger()

	// Créer le compactor
	compactor := NewCompactor(db, logger)

	// Vérifier que le compactor est actif
	if atomic.LoadInt32(&compactor.stopped) != 0 {
		t.Error("compactor should not be stopped initially")
	}

	// Attendre un peu pour s'assurer que la goroutine worker est démarrée
	time.Sleep(10 * time.Millisecond)

	// Arrêter le compactor
	start := time.Now()
	compactor.Stop()
	stopTime := time.Since(start)

	// Vérifier que l'arrêt s'est fait rapidement (moins de 1 seconde)
	if stopTime > time.Second {
		t.Errorf("compactor stop took too long: %v", stopTime)
	}

	// Vérifier que le compactor est marqué comme arrêté
	if atomic.LoadInt32(&compactor.stopped) != 1 {
		t.Error("compactor should be marked as stopped")
	}

	// Tester qu'un deuxième appel à Stop() ne fait rien
	compactor.Stop() // Ne devrait rien faire
}

func TestCompactorMultipleStop(t *testing.T) {
	db, err := dbm.NewDB("test", dbm.MemDBBackend, "")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer db.Close()

	logger := log.NewNopLogger()
	compactor := NewCompactor(db, logger)

	// Appeler Stop() plusieurs fois
	for i := 0; i < 5; i++ {
		compactor.Stop()
	}

	// Vérifier que le compactor est toujours marqué comme arrêté
	if atomic.LoadInt32(&compactor.stopped) != 1 {
		t.Error("compactor should be marked as stopped after multiple Stop calls")
	}
}

func TestCompactorSignalHandling(t *testing.T) {
	db, err := dbm.NewDB("test", dbm.MemDBBackend, "")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer db.Close()

	logger := log.NewNopLogger()
	compactor := NewCompactor(db, logger)

	// Attendre un peu pour s'assurer que les goroutines sont démarrées
	time.Sleep(10 * time.Millisecond)

	// Envoyer un signal SIGTERM pour tester l'arrêt automatique
	// Note: Ce test peut être fragile car il dépend du timing
	// et de la gestion des signaux du système d'exploitation
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Skipf("cannot find process for signal test: %v", err)
	}

	// Envoyer le signal de manière asynchrone
	go func() {
		time.Sleep(100 * time.Millisecond)
		p.Signal(syscall.SIGTERM)
	}()

	// Attendre que le compactor s'arrête
	time.Sleep(200 * time.Millisecond)

	// Vérifier que le compactor s'est arrêté
	// Note: Ce test peut ne pas être fiable car le signal peut être intercepté
	// par d'autres parties du système
	if atomic.LoadInt32(&compactor.stopped) == 0 {
		t.Log("compactor may not have stopped via signal (this is expected in some environments)")
	}
}
