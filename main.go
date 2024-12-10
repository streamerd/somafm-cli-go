package main

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/net/html/charset"
)

// Channel represents a SomaFM channel
type Channel struct {
	ID          string `xml:"id,attr"`
	Title       string `xml:"title"`
	Description string `xml:"description"`
	Genre       string `xml:"genre"`
	Image       string `xml:"image"`
	DJ          string `xml:"dj"`
	Listeners   int    `xml:"listeners"`
	FastPLS     string `xml:"fastpls"`
}

// Channels represents the root XML element
type Channels struct {
	XMLName  xml.Name  `xml:"channels"`
	Channels []Channel `xml:"channel"`
}

type playerState int

const (
	stopped playerState = iota
	playing
	titleWidth = 30
	genreWidth = 25
	statsWidth = 10
)

// model represents the application state
type model struct {
	channels    []Channel
	cursor      int
	selected    *Channel
	err         error
	loading     bool
	playerState playerState
	player      *exec.Cmd
}

// Define some basic styling
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF6B6B"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4ECDC4"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF0000"))
)

func initialModel() model {
	return model{
		loading: true,
	}
}

// Message types
type channelsMsg []Channel
type errMsg struct{ error }
type startPlaybackMsg struct {
	player *exec.Cmd
}
type stopPlaybackMsg struct{}
type playbackErrorMsg struct{ error }

func fetchChannels() tea.Msg {
	resp, err := http.Get("https://somafm.com/channels.xml")
	if err != nil {
		return errMsg{err}
	}
	defer resp.Body.Close()

	decoder := xml.NewDecoder(resp.Body)
	decoder.CharsetReader = charset.NewReaderLabel

	var channels Channels
	if err := decoder.Decode(&channels); err != nil {
		return errMsg{err}
	}

	for i := range channels.Channels {
		channels.Channels[i].Description = strings.TrimSpace(channels.Channels[i].Description)
		if fastPLS := channels.Channels[i].FastPLS; fastPLS != "" {
			if idx := strings.Index(fastPLS, "\n"); idx != -1 {
				channels.Channels[i].FastPLS = fastPLS[:idx]
			}
		}
	}

	return channelsMsg(channels.Channels)
}

// Function to parse PLS and get stream URL
func getStreamURL(plsURL string) (string, error) {
	resp, err := http.Get(plsURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	re := regexp.MustCompile(`File1=(.+)`)

	for scanner.Scan() {
		if matches := re.FindStringSubmatch(scanner.Text()); len(matches) > 1 {
			return matches[1], nil
		}
	}

	return "", fmt.Errorf("no stream URL found in PLS file")
}

// Command to start playback using MPV
func startPlayback(streamURL string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("mpv", streamURL, "--no-terminal")
		if err := cmd.Start(); err != nil {
			return playbackErrorMsg{err}
		}
		return startPlaybackMsg{cmd}
	}
}

// Command to stop playback
func stopPlayback(cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		if cmd != nil && cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil {
				return playbackErrorMsg{err}
			}
		}
		return stopPlaybackMsg{}
	}
}

func (m model) Init() tea.Cmd {
	return fetchChannels
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.player != nil {
				return m, tea.Sequence(
					stopPlayback(m.player),
					tea.Quit,
				)
			}
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			if m.cursor < len(m.channels)-1 {
				m.cursor++
			}

		case "enter", " ":
			if m.playerState == playing && m.selected == &m.channels[m.cursor] {
				m.selected = nil
				return m, stopPlayback(m.player)
			}

			var cmds []tea.Cmd
			if m.player != nil {
				cmds = append(cmds, stopPlayback(m.player))
			}

			m.selected = &m.channels[m.cursor]
			streamURL, err := getStreamURL(m.selected.FastPLS)
			if err != nil {
				return m, func() tea.Msg {
					return errMsg{err}
				}
			}

			cmds = append(cmds, startPlayback(streamURL))
			return m, tea.Sequence(cmds...)
		}

	case startPlaybackMsg:
		m.playerState = playing
		m.player = msg.player

	case stopPlaybackMsg:
		m.playerState = stopped
		m.player = nil

	case playbackErrorMsg:
		m.err = msg.error
		m.playerState = stopped
		m.player = nil

	case channelsMsg:
		m.channels = msg
		m.loading = false

	case errMsg:
		m.err = msg.error
		m.loading = false
	}

	return m, nil
}

func (m model) View() string {
	if m.loading {
		return "Loading channels...\n"
	}

	if m.err != nil {
		return errorStyle.Render(fmt.Sprintf("Error: %v\n", m.err))
	}

	s := titleStyle.Render("🎵 SomaFM Channels\n\n")

	for i, channel := range m.channels {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		title := channel.Title
		if len(title) > titleWidth-3 {
			title = title[:titleWidth-3] + "..."
		}
		title = fmt.Sprintf("%-*s", titleWidth, title)

		genre := channel.Genre
		if len(genre) > genreWidth-3 {
			genre = genre[:genreWidth-3] + "..."
		}
		genre = fmt.Sprintf("%-*s", genreWidth, genre)

		line := fmt.Sprintf("%s%s %s [%d]\n",
			cursor,
			title,
			genre,
			channel.Listeners)

		if i == m.cursor {
			line = selectedStyle.Render(line)
		}

		s += line
	}

	if m.playerState == playing && m.selected != nil {
		s += "\n" + titleStyle.Render(fmt.Sprintf("Now Playing: %s", m.selected.Title))
	}

	s += "\n(↑/↓) Navigate • (enter) Play/Stop • (q) Quit\n"

	return s
}

func main() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
