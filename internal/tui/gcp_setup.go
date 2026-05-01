package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/charon/internal/providers/gcp"
)

// GCPSetupClient is the subset of gcp.Client the TUI calls. Defined
// as an interface so tests can drop in a stub without standing up an
// httptest server. Production wires *gcp.Client.
type GCPSetupClient interface {
	ListProjects(ctx context.Context) ([]gcp.Project, error)
	CreateProject(ctx context.Context, projectID, displayName string, parent *gcp.Parent) (*gcp.Operation, error)
	WaitOperation(ctx context.Context, opName string, pollInterval time.Duration) error
	BatchEnableServices(ctx context.Context, projectID string, services []string) error
	GetBillingInfo(ctx context.Context, projectID string) (*gcp.BillingInfo, error)
}

type gcpSetupState int

const (
	gcpStateLoading gcpSetupState = iota
	gcpStatePickingProject
	gcpStateEditingNewName
	gcpStateCreatingProject
	gcpStateEnabling
	gcpStateBillingCheck
	gcpStatePickingRegion
	gcpStateError
)

// gcpSetupModel drives the M3 project-management flow inside the TUI.
// State machine mirrors gcp.Setup but message-driven for bubbletea.
//
//	loading ──projects──▶ pickingProject ──existing──▶ enabling
//	    │                       │
//	   err                      └──"n"──▶ editingNewName ──enter──▶ creatingProject
//	    │                                                                │
//	    ▼                                                               done
//	  error                                                              ▼
//	                                                               enabling
//	                                                                  │
//	                                                                 done
//	                                                                  ▼
//	                                                             billingCheck
//	                                                                  │
//	                                                                 done
//	                                                                  ▼
//	                                                             pickingRegion
//	                                                                  │
//	                                                                enter
//	                                                                  ▼
//	                                                             gcpSetupDoneMsg
//
// All async ops emit gcpSetup*DoneMsg; the model advances on receipt.
type gcpSetupModel struct {
	client  GCPSetupClient
	account string

	state gcpSetupState

	// pinnedProject is shown at the top of the picker even if Google's
	// projects.list doesn't return it. Set when the user already has a
	// configured project (cred.GCP). Closes the eventual-consistency
	// gap where a freshly-created project hasn't yet propagated to
	// projects.list — the user still sees their pick in the list
	// immediately on re-entry.
	pinnedProject *gcp.Project

	// Project picker state.
	projects   []gcp.Project
	projectCur int
	nameInput  textinput.Model

	// Result accumulator.
	chosenProject  gcp.Project
	createdNew     bool
	billingEnabled bool

	// Region picker state. Cursor index into gcp.SupportedVertexRegions
	// with -1 meaning "default" sentinel.
	regionCur int

	notice string // transient status (e.g. "creating project...")
	err    error
}

// Async messages
type gcpProjectsLoadedMsg struct {
	projects []gcp.Project
	err      error
}
type gcpProjectCreatedMsg struct {
	projectID   string
	projectName string
	err         error
}
type gcpServicesEnabledMsg struct {
	err error
}
type gcpBillingCheckedMsg struct {
	enabled bool
	err     error // non-fatal: surfaced as a notice, not an abort
}

// gcpSetupDoneMsg signals the top-level model to persist the result
// and return to scopes view.
type gcpSetupDoneMsg struct {
	account     string
	projectID   string
	projectName string
	region      string
	createdNew  bool
	billing     bool
}

// gcpSetupCancelMsg signals user-initiated cancel from any state.
type gcpSetupCancelMsg struct{}

// gcpSetupRequestMsg is emitted by scopesModel when the user presses
// enter on a realized cloud-platform row. The top-level model picks
// it up and opens the GCP setup screen.
type gcpSetupRequestMsg struct {
	account string
}

func newGCPSetupModel(client GCPSetupClient, account string, pinned *gcp.Project) gcpSetupModel {
	ti := textinput.New()
	ti.Placeholder = "Charon Gemini"
	ti.Prompt = "Display name: "
	ti.CharLimit = 30
	ti.Width = 40

	return gcpSetupModel{
		client:        client,
		account:       account,
		state:         gcpStateLoading,
		nameInput:     ti,
		regionCur:     0, // default to first region (us-central1)
		pinnedProject: pinned,
	}
}

// initCmd kicks off the project list. Returned as the first cmd when
// transitioning into screenGCPSetup.
func (m *gcpSetupModel) initCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		projects, err := client.ListProjects(ctx)
		return gcpProjectsLoadedMsg{projects: projects, err: err}
	}
}

func (m gcpSetupModel) Update(msg tea.Msg) (gcpSetupModel, tea.Cmd) {
	// ctrl+c quits the program from any sub-state, matching the rest of
	// the TUI. esc is the softer "cancel back to scope view" path
	// handled per-state below.
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch msg := msg.(type) {
	case gcpProjectsLoadedMsg:
		if msg.err != nil {
			m.state = gcpStateError
			m.err = msg.err
			return m, nil
		}
		m.projects = mergePinned(msg.projects, m.pinnedProject)
		m.state = gcpStatePickingProject
		return m, nil

	case gcpProjectCreatedMsg:
		if msg.err != nil {
			m.state = gcpStateError
			m.err = msg.err
			return m, nil
		}
		m.chosenProject = gcp.Project{
			ProjectID:      msg.projectID,
			Name:           msg.projectName,
			LifecycleState: "ACTIVE",
		}
		m.createdNew = true
		m.state = gcpStateEnabling
		m.notice = fmt.Sprintf("Enabling APIs on %s...", m.chosenProject.ProjectID)
		return m, m.enableServicesCmd()

	case gcpServicesEnabledMsg:
		if msg.err != nil {
			m.state = gcpStateError
			m.err = msg.err
			return m, nil
		}
		m.state = gcpStateBillingCheck
		m.notice = fmt.Sprintf("Checking billing on %s...", m.chosenProject.ProjectID)
		return m, m.checkBillingCmd()

	case gcpBillingCheckedMsg:
		// Billing read failure is non-fatal — record and proceed.
		m.billingEnabled = msg.enabled
		m.state = gcpStatePickingRegion
		if msg.err != nil {
			m.notice = fmt.Sprintf("Couldn't read billing info (%v) — proceeding anyway.", msg.err)
		} else if !msg.enabled {
			m.notice = "Billing not linked. Vertex calls return BILLING_DISABLED until you link a billing account."
		} else {
			m.notice = ""
		}
		return m, nil
	}

	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}

	switch m.state {
	case gcpStatePickingProject:
		return m.updatePickingProject(keyMsg)
	case gcpStateEditingNewName:
		return m.updateEditingNewName(keyMsg)
	case gcpStatePickingRegion:
		return m.updatePickingRegion(keyMsg)
	case gcpStateError:
		// Any key dismisses the error and cancels.
		return m, func() tea.Msg { return gcpSetupCancelMsg{} }
	case gcpStateLoading, gcpStateCreatingProject, gcpStateEnabling, gcpStateBillingCheck:
		// Async ops in flight: only esc cancels (ctrl+c handled at top).
		if keyMsg.String() == "esc" {
			return m, func() tea.Msg { return gcpSetupCancelMsg{} }
		}
	}
	return m, nil
}

func (m gcpSetupModel) updatePickingProject(msg tea.KeyMsg) (gcpSetupModel, tea.Cmd) {
	// Cursor positions: 0..len(projects)-1 are existing projects;
	// len(projects) is the synthetic "+ new project" row.
	maxCur := len(m.projects)
	switch msg.String() {
	case "up", "k":
		if m.projectCur > 0 {
			m.projectCur--
		}
	case "down", "j":
		if m.projectCur < maxCur {
			m.projectCur++
		}
	case "enter":
		if m.projectCur == maxCur {
			// "+ new project" row.
			m.state = gcpStateEditingNewName
			m.nameInput.SetValue("")
			m.nameInput.Focus()
			return m, nil
		}
		m.chosenProject = m.projects[m.projectCur]
		m.createdNew = false
		m.state = gcpStateEnabling
		m.notice = fmt.Sprintf("Enabling APIs on %s...", m.chosenProject.ProjectID)
		return m, m.enableServicesCmd()
	case "esc":
		return m, func() tea.Msg { return gcpSetupCancelMsg{} }
	}
	return m, nil
}

func (m gcpSetupModel) updateEditingNewName(msg tea.KeyMsg) (gcpSetupModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			return m, nil
		}
		id := generateGCPProjectID()
		m.state = gcpStateCreatingProject
		m.notice = fmt.Sprintf("Creating project %q (id: %s)... 5-30s", name, id)
		return m, m.createProjectCmd(id, name)
	case "esc":
		// Back to project picker.
		m.state = gcpStatePickingProject
		m.nameInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m gcpSetupModel) updatePickingRegion(msg tea.KeyMsg) (gcpSetupModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.regionCur > 0 {
			m.regionCur--
		}
	case "down", "j":
		if m.regionCur < len(gcp.SupportedVertexRegions)-1 {
			m.regionCur++
		}
	case "enter":
		region := gcp.SupportedVertexRegions[m.regionCur]
		account := m.account
		project := m.chosenProject
		createdNew := m.createdNew
		billing := m.billingEnabled
		return m, func() tea.Msg {
			return gcpSetupDoneMsg{
				account:     account,
				projectID:   project.ProjectID,
				projectName: project.Name,
				region:      region,
				createdNew:  createdNew,
				billing:     billing,
			}
		}
	case "esc":
		return m, func() tea.Msg { return gcpSetupCancelMsg{} }
	}
	return m, nil
}

func (m gcpSetupModel) createProjectCmd(id, name string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		op, err := client.CreateProject(ctx, id, name, nil)
		if err != nil {
			return gcpProjectCreatedMsg{err: err}
		}
		if !op.Done {
			if err := client.WaitOperation(ctx, op.Name, 0); err != nil {
				return gcpProjectCreatedMsg{err: err}
			}
		}
		return gcpProjectCreatedMsg{projectID: id, projectName: name}
	}
}

func (m gcpSetupModel) enableServicesCmd() tea.Cmd {
	client := m.client
	projectID := m.chosenProject.ProjectID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		err := client.BatchEnableServices(ctx, projectID, gcp.RequiredServices)
		return gcpServicesEnabledMsg{err: err}
	}
}

func (m gcpSetupModel) checkBillingCmd() tea.Cmd {
	client := m.client
	projectID := m.chosenProject.ProjectID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		info, err := client.GetBillingInfo(ctx, projectID)
		if err != nil {
			return gcpBillingCheckedMsg{enabled: false, err: err}
		}
		return gcpBillingCheckedMsg{enabled: info.BillingEnabled}
	}
}

func (m gcpSetupModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Google Cloud setup — %s", m.account)))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")

	switch m.state {
	case gcpStateLoading:
		b.WriteString("  Loading your Google Cloud projects...\n")
	case gcpStatePickingProject:
		b.WriteString("  Pick a project:\n\n")
		for i, p := range m.projects {
			cursor := "  "
			if i == m.projectCur {
				cursor = "> "
			}
			fmt.Fprintf(&b, "  %s%-30s  %s\n", cursor, p.ProjectID, p.Name)
		}
		// Synthetic "+ new project" row at index len(m.projects).
		newCursor := "  "
		if m.projectCur == len(m.projects) {
			newCursor = "> "
		}
		fmt.Fprintf(&b, "  %s+ new project\n", newCursor)
	case gcpStateEditingNewName:
		b.WriteString("  New Google Cloud project\n\n")
		b.WriteString("  ")
		b.WriteString(m.nameInput.View())
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  enter: create    esc: back"))
	case gcpStateCreatingProject, gcpStateEnabling, gcpStateBillingCheck:
		b.WriteString("  ")
		b.WriteString(m.notice)
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  esc: cancel"))
	case gcpStatePickingRegion:
		b.WriteString(fmt.Sprintf("  Project: %s (%s)\n", m.chosenProject.ProjectID, m.chosenProject.Name))
		if m.notice != "" {
			b.WriteString("\n  ")
			b.WriteString(m.notice)
			b.WriteString("\n")
		}
		b.WriteString("\n  Pick a Vertex AI region:\n\n")
		for i, r := range gcp.SupportedVertexRegions {
			cursor := "  "
			if i == m.regionCur {
				cursor = "> "
			}
			marker := " "
			if r == gcp.DefaultVertexRegion {
				marker = "*"
			}
			fmt.Fprintf(&b, "  %s%s %s\n", cursor, marker, r)
		}
	case gcpStateError:
		b.WriteString(rowDelStyle.Render(fmt.Sprintf("  %v\n", m.err)))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  press any key to dismiss"))
	}

	b.WriteString("\n\n")
	switch m.state {
	case gcpStatePickingProject:
		b.WriteString(helpStyle.Render("  ↑↓ nav   enter pick   esc cancel"))
	case gcpStatePickingRegion:
		b.WriteString(helpStyle.Render("  ↑↓ nav   enter pick   esc cancel"))
	}
	return b.String()
}

// mergePinned ensures pinned (if non-nil) appears in the returned
// list, prepended when projects.list hasn't caught up to a recent
// create. Already-listed pinned projects are left in place — no
// duplication, original order preserved.
func mergePinned(listed []gcp.Project, pinned *gcp.Project) []gcp.Project {
	if pinned == nil {
		return listed
	}
	for _, p := range listed {
		if p.ProjectID == pinned.ProjectID {
			return listed
		}
	}
	out := make([]gcp.Project, 0, len(listed)+1)
	out = append(out, *pinned)
	out = append(out, listed...)
	return out
}

// generateGCPProjectID mirrors the orchestrator's generator. Defined
// here too so the TUI doesn't need to call into orchestrator code.
// Keep in sync with internal/providers/gcp/setup.go::generateProjectID.
func generateGCPProjectID() string {
	// Reuse the package-level helper via a tiny wrapper to avoid
	// duplicating crypto/rand boilerplate. Calling into the gcp
	// package here is fine — TUI already imports it.
	return gcp.GenerateProjectID()
}
