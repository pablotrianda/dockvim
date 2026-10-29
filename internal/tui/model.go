package tui

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"dockvim/internal/dockercli"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type viewMode int

const (
	containersView viewMode = iota
	projectsView
)

type item struct {
	kind      string
	container dockercli.Container
	project   dockercli.Project
}

type refreshMsg struct {
	containers []dockercli.Container
	err        error
}
type actionMsg struct {
	message string
	err     error
}
type logsMsg struct {
	text string
	err  error
}
type tickMsg time.Time

type Model struct {
	containers    []dockercli.Container
	projects      []dockercli.Project
	items         []item
	selected      int
	mode          viewMode
	width, height int
	searching     bool
	search        string
	confirm       string
	pending       item
	detail        string
	status        string
	err           string
	loading       bool
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#A6E3A1"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6ADC8"))
	selectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("#313244")).Foreground(lipgloss.Color("#CBA6F7")).Bold(true)
	runningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1"))
	stoppedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8"))
	borderStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#45475A")).Padding(0, 1)
)

func NewModel() Model {
	return Model{loading: true, status: "Conectando con Docker…", width: 100, height: 30}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(), tickCmd())
}

func refreshCmd() tea.Cmd {
	return func() tea.Msg {
		cs, err := dockercli.ListContainers()
		return refreshMsg{containers: cs, err: err}
	}
}
func tickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) rebuildItems() {
	m.projects = dockercli.ListProjects(m.containers)
	m.items = nil
	if m.mode == containersView {
		for _, c := range m.containers {
			m.items = append(m.items, item{kind: "container", container: c})
		}
	} else {
		for _, p := range m.projects {
			m.items = append(m.items, item{kind: "project", project: p})
		}
	}
	if m.search != "" {
		filtered := make([]item, 0, len(m.items))
		q := strings.ToLower(m.search)
		for _, it := range m.items {
			if strings.Contains(strings.ToLower(it.name()), q) {
				filtered = append(filtered, it)
			}
		}
		m.items = filtered
	}
	if len(m.items) == 0 {
		m.selected = 0
	} else if m.selected >= len(m.items) {
		m.selected = len(m.items) - 1
	}
}

func (it item) name() string {
	if it.kind == "project" {
		return it.project.Name
	}
	return it.container.Names
}
func (it item) state() string {
	if it.kind == "project" {
		running := 0
		for _, c := range it.project.Containers {
			if c.State == "running" {
				running++
			}
		}
		return fmt.Sprintf("%d/%d running", running, len(it.project.Containers))
	}
	if it.container.State != "" {
		return it.container.State
	}
	return it.container.Status
}
var publishedPortRE = regexp.MustCompile(`(?:\[[^]]+\]|[^,:]+)?:(\d+)->(\d+)/(tcp|udp)`)

func compactPorts(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "—"
	}
	seen := map[string]bool{}
	var ports []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		matches := publishedPortRE.FindStringSubmatch(entry)
		if len(matches) == 4 {
			value := matches[1] + "→" + matches[2]
			if matches[3] == "udp" {
				value += "/udp"
			}
			if !seen[value] {
				seen[value] = true
				ports = append(ports, value)
			}
		}
	}
	if len(ports) == 0 {
		// Docker may list container-only ports such as "5432/tcp"; they
		// are not published on the host, so don't imply they are reachable.
		return "—"
	}
	return strings.Join(ports, ",")
}

func (it item) ports() string {
	if it.kind == "project" {
		seen := map[string]bool{}
		var ports []string
		for _, c := range it.project.Containers {
			for _, port := range strings.Split(compactPorts(c.Ports), ",") {
				port = strings.TrimSpace(port)
				if port != "" && port != "—" && !seen[port] {
					seen[port] = true
					ports = append(ports, port)
				}
			}
		}
		if len(ports) == 0 {
			return "—"
		}
		return strings.Join(ports, ",")
	}
	return compactPorts(it.container.Ports)
}

func clipText(value string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxWidth {
		return value
	}
	if maxWidth == 1 {
		return "…"
	}
	return string(runes[:maxWidth-1]) + "…"
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case refreshMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			m.status = "No se pudo consultar Docker"
		} else {
			m.containers = msg.containers
			m.rebuildItems()
			m.status = fmt.Sprintf("%d contenedores · %d proyectos Compose", len(m.containers), len(m.projects))
			m.err = ""
		}
	case tickMsg:
		return m, tea.Batch(refreshCmd(), tickCmd())
	case actionMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			m.status = "La acción falló"
		} else {
			m.err = ""
			m.status = msg.message
		}
		return m, refreshCmd()
	case logsMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.detail = msg.text
		}
	case tea.KeyMsg:
		key := msg.String()
		if m.confirm != "" {
			switch key {
			case "y", "Y", "enter":
				action := m.confirm
				it := m.pending
				m.confirm = ""
				return m, runAction(it, action)
			default:
				m.confirm = ""
				m.status = "Acción cancelada"
			}
			return m, nil
		}
		if m.searching {
			switch key {
			case "enter", "esc":
				m.searching = false
				m.rebuildItems()
			case "backspace":
				if len(m.search) > 0 {
					m.search = m.search[:len(m.search)-1]
					m.rebuildItems()
				}
			case "ctrl+c":
				return m, tea.Quit
			default:
				if len(msg.Runes) > 0 {
					m.search += string(msg.Runes)
					m.rebuildItems()
				}
			}
			return m, nil
		}
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.selected < len(m.items)-1 {
				m.selected++
			}
		case "k", "up":
			if m.selected > 0 {
				m.selected--
			}
		case "g":
			m.selected = 0
		case "G":
			if len(m.items) > 0 {
				m.selected = len(m.items) - 1
			}
		case "tab":
			if m.mode == containersView {
				m.mode = projectsView
			} else {
				m.mode = containersView
			}
			m.selected = 0
			m.rebuildItems()
		case "/":
			m.searching = true
			m.search = ""
			m.rebuildItems()
		case "R":
			m.loading = true
			return m, refreshCmd()
		case "l":
			if len(m.items) > 0 {
				it := m.items[m.selected]
				if it.kind == "container" {
					return m, liveLogs(it.container.ID)
				}
				m.detail = projectLogsHint(it.project)
			}
		case "e":
			if len(m.items) > 0 && m.items[m.selected].kind == "container" {
				return m, execShell(m.items[m.selected].container.ID)
			}
		case "s", "x", "r":
			if len(m.items) > 0 {
				it := m.items[m.selected]
				action := map[string]string{"s": "start", "x": "stop", "r": "restart"}[key]
				if it.kind == "project" {
					m.status = "Las acciones start/stop/restart se aplican a contenedores; para Compose usá u/d."
				} else if key == "x" || key == "r" {
					m.confirm, m.pending = action, it
				} else {
					return m, runAction(it, action)
				}
			}
		case "u", "d":
			if len(m.items) > 0 {
				it := m.items[m.selected]
				if it.kind != "project" {
					m.status = "Seleccioná un proyecto en la vista Compose (Tab)."
					break
				}
				action := "up"
				if key == "d" {
					action = "down"
				}
				m.confirm, m.pending = action, it
			}
		case "L":
			if len(m.items) > 0 && m.items[m.selected].kind == "container" {
				return m, recentLogs(m.items[m.selected].container.ID)
			}
		}
	}
	return m, nil
}

func runAction(it item, action string) tea.Cmd {
	return func() tea.Msg {
		if it.kind == "project" {
			err := dockercli.ComposeAction(it.project, action)
			return actionMsg{message: fmt.Sprintf("Compose %s: %s", action, it.project.Name), err: err}
		}
		err := dockercli.ContainerAction(action, it.container.ID)
		return actionMsg{message: fmt.Sprintf("%s: %s", action, it.container.Names), err: err}
	}
}
func recentLogs(id string) tea.Cmd {
	return func() tea.Msg { s, err := dockercli.RecentLogs(id); return logsMsg{text: s, err: err} }
}

// Interactive commands are run as Bubble Tea subprocesses, temporarily handing
// the terminal to Docker and restoring the TUI when the command exits.
func liveLogs(itID string) tea.Cmd {
	c := exec.Command("docker", "logs", "-f", "--tail", "100", itID)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return actionMsg{message: "docker logs terminó", err: err}
		}
		return actionMsg{message: "Volviste de logs", err: nil}
	})
}
func execShell(id string) tea.Cmd {
	c := exec.Command("docker", "exec", "-it", id, "sh")
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return actionMsg{message: "docker exec terminó", err: err}
		}
		return actionMsg{message: "Volviste de la shell", err: nil}
	})
}

func describe(it item) string {
	if it.kind == "project" {
		p := it.project
		lines := []string{"PROYECTO COMPOSE", "", "Nombre: " + p.Name, "Directorio: " + fallback(p.Directory, "no informado"), "Archivos Compose: " + strings.Join(p.Compose, ", "), "", "Contenedores:"}
		for _, c := range p.Containers {
			lines = append(lines, fmt.Sprintf("  • %s  [%s]  %s", c.Names, c.State, c.Ports))
		}
		return strings.Join(lines, "\n")
	}
	c := it.container
	return strings.Join([]string{
		"CONTENEDOR", "", "Nombre: " + c.Names, "ID: " + shortID(c.ID), "Estado: " + c.Status,
		"Imagen: " + c.Image, "Comando: " + c.Command, "Creado: " + c.Created, "Puertos: " + fallback(c.Ports, "—"),
		"Compose: " + fallback(c.Labels["com.docker.compose.project"], "no"), "Compose dir: " + fallback(c.Labels["com.docker.compose.project.working_dir"], "—"),
		"Compose files: " + fallback(c.Labels["com.docker.compose.project.config_files"], "—"),
	}, "\n")
}
func projectLogsHint(p dockercli.Project) string {
	return "LOGS DEL PROYECTO\n\nElegí un contenedor en la vista Contenedores (Tab) y presioná l para seguir logs en vivo.\n\nContenedores:\n" + func() string {
		var a []string
		for _, c := range p.Containers {
			a = append(a, "• "+c.Names+" ("+c.State+")")
		}
		return strings.Join(a, "\n")
	}()
}
func fallback(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}
func shortID(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func (m Model) View() string {
	if m.width < 40 {
		return "dockvim necesita una terminal de al menos 40 columnas.\nPresioná q para salir."
	}

	header := titleStyle.Render("dockvim") + mutedStyle.Render("  Docker TUI") + "\n" +
		mutedStyle.Render(fmt.Sprintf("%d contenedores  ·  %d proyectos Compose", len(m.containers), len(m.projects)))
	mode := "CONTENEDORES"
	if m.mode == projectsView {
		mode = "COMPOSE"
	}
	listTitle := titleStyle.Render(mode)
	if m.searching {
		listTitle += "  /" + m.search + "_"
	}

	// Keep state and port columns reserved before giving the remaining width to names.
	innerWidth := m.width - 8
	portsWidth := 16
	stateWidth := 12
	if innerWidth < 58 {
		portsWidth = 12
		stateWidth = 10
	}
	nameWidth := innerWidth - stateWidth - portsWidth - 4
	if nameWidth < 8 {
		nameWidth = 8
	}
	headerRow := fmt.Sprintf("%-*s %-*s %s",
		nameWidth, clipText("NOMBRE", nameWidth),
		stateWidth, clipText("ESTADO", stateWidth),
		clipText("PUERTOS", portsWidth))
	lines := []string{listTitle, mutedStyle.Render(headerRow)}

	for i, it := range m.items {
		name := clipText(it.name(), nameWidth)
		state := clipText(it.state(), stateWidth)
		ports := clipText(it.ports(), portsWidth)
		line := fmt.Sprintf("%-*s %-*s %s", nameWidth, name, stateWidth, state, ports)
		if i == m.selected {
			line = selectedStyle.Width(m.width - 6).Render("› " + line)
		} else if strings.Contains(it.state(), "running") {
			line = runningStyle.Render("  " + line)
		} else {
			line = stoppedStyle.Render("  " + line)
		}
		lines = append(lines, line)
	}
	if len(m.items) == 0 {
		lines = append(lines, mutedStyle.Render("  Sin elementos"))
	}

	panel := borderStyle.Width(m.width - 4).Height(m.height - 9).Render(strings.Join(lines, "\n"))
	help1 := mutedStyle.Render("j/k mover  Tab vista  / buscar  l logs  L recientes")
	help2 := mutedStyle.Render("e shell  s start  x stop  r restart  u up  d down  R actualizar  q salir")
	status := m.status
	if m.err != "" {
		status = stoppedStyle.Render("Error: " + m.err)
	}
	if m.confirm != "" {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Bold(true).Render(
			fmt.Sprintf("¿Confirmar %s sobre %s? [y/N]", m.confirm, m.pending.name()))
	}
	if status == "" {
		status = "Listo"
	}
	return header + "\n\n" + panel + "\n\n" + help1 + "\n" + help2 + "\n" + status + "\n"
}

func init() { _ = os.Getenv("DOCKVIM_UNUSED") }
