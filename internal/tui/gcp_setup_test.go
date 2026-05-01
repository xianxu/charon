package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/providers/gcp"
)

// fakeGCPClient drives gcpSetupModel through its states without
// touching the network. Each method captures inputs and returns
// pre-canned responses; tests assemble per-scenario.
type fakeGCPClient struct {
	listResult    []gcp.Project
	listErr       error
	createOp      *gcp.Operation
	createErr     error
	waitErr       error
	enableErr     error
	billing       *gcp.BillingInfo
	billingErr    error

	listCalls    int
	createCalls  int
	waitCalls    int
	enableCalls  int
	billingCalls int
}

func (f *fakeGCPClient) ListProjects(ctx context.Context) ([]gcp.Project, error) {
	f.listCalls++
	return f.listResult, f.listErr
}
func (f *fakeGCPClient) CreateProject(ctx context.Context, projectID, displayName string, parent *gcp.Parent) (*gcp.Operation, error) {
	f.createCalls++
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createOp, nil
}
func (f *fakeGCPClient) WaitOperation(ctx context.Context, opName string, pollInterval time.Duration) error {
	f.waitCalls++
	return f.waitErr
}
func (f *fakeGCPClient) BatchEnableServices(ctx context.Context, projectID string, services []string) error {
	f.enableCalls++
	return f.enableErr
}
func (f *fakeGCPClient) GetBillingInfo(ctx context.Context, projectID string) (*gcp.BillingInfo, error) {
	f.billingCalls++
	return f.billing, f.billingErr
}

// runCmd executes a tea.Cmd synchronously and returns the resulting
// message. Bubbletea normally schedules cmds on the program loop; in
// tests we just want the message.
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func TestGCPSetup_PickExistingFlowEmitsDoneMsg(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{
			{ProjectID: "alpha", Name: "Alpha", LifecycleState: "ACTIVE"},
		},
		billing: &gcp.BillingInfo{BillingEnabled: true},
	}
	m := newGCPSetupModel(fake, "user@gmail.com")
	if msg := runCmd(m.initCmd()); msg != nil {
		var cmd tea.Cmd
		m, cmd = m.Update(msg)
		_ = cmd
	}
	if m.state != gcpStatePickingProject {
		t.Fatalf("after list, state = %d, want pickingProject", m.state)
	}

	// Press enter to pick alpha.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != gcpStateEnabling {
		t.Fatalf("after pick, state = %d, want enabling", m.state)
	}
	// Run the enable cmd, feed result back.
	m, _ = m.Update(runCmd(cmd))
	if m.state != gcpStateBillingCheck {
		t.Fatalf("after enable, state = %d, want billingCheck", m.state)
	}

	// Run the billing cmd, feed result back. Need the billing cmd —
	// it was returned by the gcpServicesEnabledMsg handler.
	// Re-issue from the model.
	billingMsg := m.checkBillingCmd()()
	m, _ = m.Update(billingMsg)
	if m.state != gcpStatePickingRegion {
		t.Fatalf("after billing, state = %d, want pickingRegion", m.state)
	}

	// Press enter on default region (cursor already on us-central1).
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	doneMsg, ok := runCmd(cmd).(gcpSetupDoneMsg)
	if !ok {
		t.Fatalf("expected gcpSetupDoneMsg, got %T", runCmd(cmd))
	}
	if doneMsg.account != "user@gmail.com" {
		t.Errorf("account = %q", doneMsg.account)
	}
	if doneMsg.projectID != "alpha" {
		t.Errorf("projectID = %q", doneMsg.projectID)
	}
	if doneMsg.region != gcp.DefaultVertexRegion {
		t.Errorf("region = %q, want %s", doneMsg.region, gcp.DefaultVertexRegion)
	}
	if doneMsg.createdNew {
		t.Error("createdNew should be false for existing pick")
	}
	if !doneMsg.billing {
		t.Error("billing should be true")
	}
}

func TestGCPSetup_ListErrorTransitionsToError(t *testing.T) {
	fake := &fakeGCPClient{listErr: errors.New("403 forbidden")}
	m := newGCPSetupModel(fake, "user@gmail.com")
	m, _ = m.Update(runCmd(m.initCmd()))
	if m.state != gcpStateError {
		t.Fatalf("state = %d, want error", m.state)
	}
	if !strings.Contains(m.err.Error(), "403") {
		t.Errorf("err missing context: %v", m.err)
	}
}

func TestGCPSetup_CreateNewProjectFlow(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: nil,
		createOp:   &gcp.Operation{Name: "operations/x", Done: true},
		billing:    &gcp.BillingInfo{BillingEnabled: false},
	}
	m := newGCPSetupModel(fake, "user@gmail.com")
	m, _ = m.Update(runCmd(m.initCmd()))

	// Press n to start new-project flow.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.state != gcpStateEditingNewName {
		t.Fatalf("state = %d, want editingNewName", m.state)
	}

	// Type a name. textinput accepts runes via tea.KeyMsg{Type: KeyRunes}.
	m.nameInput.SetValue("My Charon")

	// Press enter to create.
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != gcpStateCreatingProject {
		t.Fatalf("state = %d, want creatingProject", m.state)
	}
	// Run the create cmd. createOp.Done=true means no Wait call.
	m, _ = m.Update(runCmd(cmd))
	if m.state != gcpStateEnabling {
		t.Fatalf("after create, state = %d, want enabling", m.state)
	}
	if !m.createdNew {
		t.Error("createdNew should be true after projects.create")
	}
	if fake.waitCalls != 0 {
		t.Error("Wait should not be called when op is already Done")
	}
}

func TestGCPSetup_BillingReadFailureNonFatal(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billingErr: errors.New("permission denied"),
	}
	m := newGCPSetupModel(fake, "u@gmail.com")
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick project
	m, _ = m.Update(runCmd(cmd))                       // enable result
	m, _ = m.Update(m.checkBillingCmd()())             // billing result
	if m.state != gcpStatePickingRegion {
		t.Fatalf("state = %d, want pickingRegion (billing failure must be non-fatal)", m.state)
	}
	if !strings.Contains(m.notice, "Couldn't read billing info") {
		t.Errorf("expected billing notice, got: %q", m.notice)
	}
}

func TestGCPSetup_EscFromPickerCancels(t *testing.T) {
	fake := &fakeGCPClient{listResult: []gcp.Project{
		{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"},
	}}
	m := newGCPSetupModel(fake, "u@gmail.com")
	m, _ = m.Update(runCmd(m.initCmd()))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := runCmd(cmd).(gcpSetupCancelMsg); !ok {
		t.Errorf("expected gcpSetupCancelMsg from esc, got %T", runCmd(cmd))
	}
}

func TestGCPSetup_RegionPickerNumericNav(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billing:    &gcp.BillingInfo{BillingEnabled: true},
	}
	m := newGCPSetupModel(fake, "u@gmail.com")
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))
	m, _ = m.Update(m.checkBillingCmd()())

	// Move down twice, then enter.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	doneMsg, ok := runCmd(cmd).(gcpSetupDoneMsg)
	if !ok {
		t.Fatalf("expected done msg, got %T", runCmd(cmd))
	}
	if doneMsg.region != gcp.SupportedVertexRegions[2] {
		t.Errorf("region = %q, want %q", doneMsg.region, gcp.SupportedVertexRegions[2])
	}
}
