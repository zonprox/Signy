package fsm

import (
	"testing"

	"github.com/zonprox/Signy/internal/models"
)

func TestNewStore(t *testing.T) {
	store := NewStore()
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestGetDefault(t *testing.T) {
	store := NewStore()
	sess := store.Get(12345)
	if sess.State != models.StateIdle {
		t.Fatalf("expected idle state, got %s", sess.State)
	}
	if sess.UserID != 12345 {
		t.Fatalf("expected user id 12345, got %d", sess.UserID)
	}
}

func TestSetAndGet(t *testing.T) {
	store := NewStore()
	sess := &models.UserSession{
		UserID:      42,
		State:       models.StateCertCreateName,
		CertSetName: "my-cert",
	}
	store.Set(42, sess)

	got := store.Get(42)
	if got.State != models.StateCertCreateName {
		t.Fatalf("expected %s, got %s", models.StateCertCreateName, got.State)
	}
	if got.CertSetName != "my-cert" {
		t.Fatalf("expected 'my-cert', got %s", got.CertSetName)
	}
}

func TestClear(t *testing.T) {
	store := NewStore()
	store.Set(42, &models.UserSession{UserID: 42, State: models.StateCertCreateP12})
	store.Clear(42)

	got := store.Get(42)
	if got.State != models.StateIdle {
		t.Fatalf("expected idle after clear, got %s", got.State)
	}
}

func TestTransitionSuccess(t *testing.T) {
	store := NewStore()

	// From idle to cert_create_name
	ok := store.Transition(42, models.StateIdle, models.StateCertCreateName)
	if !ok {
		t.Fatal("transition should succeed")
	}
	if store.GetState(42) != models.StateCertCreateName {
		t.Fatal("state should be cert_create_name")
	}

	// From cert_create_name to cert_create_p12
	ok = store.Transition(42, models.StateCertCreateName, models.StateCertCreateP12)
	if !ok {
		t.Fatal("transition should succeed")
	}
	if store.GetState(42) != models.StateCertCreateP12 {
		t.Fatal("state should be cert_create_p12")
	}
}

func TestTransitionFail(t *testing.T) {
	store := NewStore()
	store.Set(42, &models.UserSession{UserID: 42, State: models.StateCertCreateName})

	// Wrong 'from' state
	ok := store.Transition(42, models.StateCertCreateP12, models.StateCertCreatePassword)
	if ok {
		t.Fatal("transition should fail for wrong 'from' state")
	}
	if store.GetState(42) != models.StateCertCreateName {
		t.Fatal("state should remain cert_create_name")
	}
}

func TestCertCreateFlow(t *testing.T) {
	store := NewStore()
	uid := int64(100)

	steps := []struct {
		from models.UserState
		to   models.UserState
	}{
		{models.StateIdle, models.StateCertCreateName},
		{models.StateCertCreateName, models.StateCertCreateP12},
		{models.StateCertCreateP12, models.StateCertCreatePassword},
		{models.StateCertCreatePassword, models.StateCertCreateProvision},
		{models.StateCertCreateProvision, models.StateIdle},
	}

	for i, step := range steps {
		ok := store.Transition(uid, step.from, step.to)
		if !ok {
			t.Fatalf("step %d: transition %s -> %s failed", i, step.from, step.to)
		}
	}
}

func TestJobCreateFlow(t *testing.T) {
	store := NewStore()
	uid := int64(200)

	steps := []struct {
		from models.UserState
		to   models.UserState
	}{
		{models.StateIdle, models.StateJobSelectCert},
		{models.StateJobSelectCert, models.StateJobUploadIPA},
		{models.StateJobUploadIPA, models.StateJobConfirm},
		{models.StateJobConfirm, models.StateIdle},
	}

	for i, step := range steps {
		ok := store.Transition(uid, step.from, step.to)
		if !ok {
			t.Fatalf("step %d: transition %s -> %s failed", i, step.from, step.to)
		}
	}
}

func TestJobCreateFlowWithPassword(t *testing.T) {
	store := NewStore()
	uid := int64(300)

	steps := []struct {
		from models.UserState
		to   models.UserState
	}{
		{models.StateIdle, models.StateJobSelectCert},
		{models.StateJobSelectCert, models.StateJobUploadIPA},
		{models.StateJobUploadIPA, models.StateJobPasswordPrompt},
		{models.StateJobPasswordPrompt, models.StateJobConfirm},
		{models.StateJobConfirm, models.StateIdle},
	}

	for i, step := range steps {
		ok := store.Transition(uid, step.from, step.to)
		if !ok {
			t.Fatalf("step %d: transition %s -> %s failed", i, step.from, step.to)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	store := NewStore()
	done := make(chan bool, 100)

	for i := 0; i < 100; i++ {
		go func(id int64) {
			store.Set(id, &models.UserSession{UserID: id, State: models.StateCertCreateP12})
			_ = store.Get(id)
			store.Clear(id)
			done <- true
		}(int64(i))
	}

	for i := 0; i < 100; i++ {
		<-done
	}
}
