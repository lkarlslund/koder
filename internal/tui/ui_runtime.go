package tui

import (
	tea "github.com/lkarlslund/koder/internal/ui/tea"

	"github.com/lkarlslund/koder/internal/tui/dialogs"
	"github.com/lkarlslund/koder/internal/ui"
)

const (
	mainWindowID         ui.WindowID = "main"
	sessionWindowID      ui.WindowID = "session-dialog"
	preferencesWindowID  ui.WindowID = "preferences-dialog"
	toolsWindowID        ui.WindowID = "tools-dialog"
	connectWindowID      ui.WindowID = "connect-dialog"
	disconnectWindowID   ui.WindowID = "disconnect-dialog"
	modelWindowID        ui.WindowID = "model-dialog"
	agentsWindowID       ui.WindowID = "agents-modal"
	helpWindowID         ui.WindowID = "help-modal"
	llmPreviewWindowID   ui.WindowID = "llm-preview"
	pickerWindowID       ui.WindowID = "picker-dialog"
)

type modelWindow struct {
	base   ui.BaseWindow
	model  *Model
	bounds func(*Model, ui.Rect) ui.Rect
	render func(*Model, ui.Rect) ui.Surface
	key    func(*Model, tea.KeyMsg) tea.Cmd
	mouse  func(*Model, tea.MouseMsg) (bool, tea.Cmd)
}

func (w *modelWindow) ID() ui.WindowID {
	return w.base.ID()
}

func (w *modelWindow) Bounds(root ui.Rect) ui.Rect {
	if w.bounds == nil {
		return root
	}
	return w.bounds(w.model, root)
}

func (w *modelWindow) ZIndex() int {
	return w.base.ZIndex()
}

func (w *modelWindow) Focusable() bool {
	return w.base.Focusable()
}

func (w *modelWindow) Visible() bool {
	return w.base.Visible()
}

func (w *modelWindow) Modal() bool {
	return w.base.Modal()
}

func (w *modelWindow) NeedsRedraw() bool {
	return w.base.NeedsRedraw()
}

func (w *modelWindow) ClearRedraw() {
	w.base.ClearRedraw()
}

func (w *modelWindow) Focus() {
	w.base.Focus()
}

func (w *modelWindow) Blur() {
	w.base.Blur()
}

func (w *modelWindow) HandleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if w.key == nil {
		return false, nil
	}
	return true, w.key(w.model, msg)
}

func (w *modelWindow) HandleMouse(msg tea.MouseMsg) (bool, tea.Cmd) {
	if w.mouse == nil {
		return false, nil
	}
	return w.mouse(w.model, msg)
}

func (w *modelWindow) Render(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	if w.render == nil {
		return ui.BlankSurface(bounds.W, bounds.H)
	}
	return w.render(w.model, bounds)
}

func (m *Model) ensureUIRoot() *ui.Root {
	if m.uiRoot == nil {
		m.uiRoot = ui.NewRoot(m.palette, ui.Rect{W: max(0, m.width), H: max(0, m.height)})
	}
	return m.uiRoot
}

func (m *Model) syncUIRoot() *ui.Root {
	root := m.ensureUIRoot()
	root.SetBounds(ui.Rect{W: max(0, m.width), H: max(0, m.height)})
	root.SetMainWindow(m.mainWindow())
	overlays := m.overlayWindows()
	root.SetWindows(overlays)
	if len(overlays) > 0 {
		root.FocusWindow(overlays[len(overlays)-1].ID())
	} else {
		root.FocusWindow(mainWindowID)
	}
	return root
}

func (m *Model) mainWindow() ui.Window {
	return &modelWindow{
		base: ui.BaseWindow{
			WindowID:      mainWindowID,
			Order:         0,
			FocusableFlag: true,
			VisibleFlag:   true,
			Dirty:         true,
		},
		model: m,
		render: func(m *Model, bounds ui.Rect) ui.Surface {
			root := ui.BlankSurface(max(0, bounds.W), max(0, bounds.H))
			body := m.renderBodySurface().Normalize(max(0, bounds.W), max(0, m.viewport.Height))
			root = root.PlaceAt(0, 0, body)
			footer := m.renderFooterSurface()
			if footerSize := footer.Size(); footerSize.H > 0 {
				root = root.PlaceAt(0, max(0, bounds.H-footerSize.H), footer.Normalize(max(0, bounds.W), footerSize.H))
			}
			return root
		},
		key: func(m *Model, msg tea.KeyMsg) tea.Cmd {
			return m.handleMainWindowKey(msg)
		},
		mouse: func(m *Model, msg tea.MouseMsg) (bool, tea.Cmd) {
			return m.handleMainWindowMouse(msg)
		},
	}
}

func (m *Model) overlayWindows() []ui.Window {
	windows := make([]ui.Window, 0, 8)
	if m.hasSessionDialog() {
		windows = append(windows, m.centeredWindow(sessionWindowID, 10, m.renderSessionDialogElement(), func(m *Model, msg tea.KeyMsg) tea.Cmd {
			_, cmd := m.handleSessionDialogKey(msg)
			return cmd
		}, func(m *Model, controlID string) tea.Cmd {
			action := m.sessionDialog.ActivateControl(controlID)
			switch action.Kind {
			case dialogs.SessionDialogActionSelect:
				m.startBusy(busyScopeSidebar, "Resuming session…")
				return tea.Batch(m.loadSessionCmd(action.SessionID), m.spinnerCmdIfNeeded())
			case dialogs.SessionDialogActionCancel:
				m.startBusy(busyScopeSidebar, "Creating session…")
				return tea.Batch(m.newSessionCmd(), m.spinnerCmdIfNeeded())
			default:
				return nil
			}
		}))
	}
	if m.hasModelDialog() {
		windows = append(windows, m.centeredWindow(modelWindowID, 20, m.renderModelDialogElement(), func(m *Model, msg tea.KeyMsg) tea.Cmd {
			_, cmd := m.handleModelDialogKey(msg)
			return cmd
		}, func(m *Model, controlID string) tea.Cmd {
			action := m.modelDialog.ActivateControl(controlID)
			switch action.Kind {
			case dialogs.ModelDialogActionSelect:
				if err := m.selectModel(action.ModelID); err != nil {
					m.status = err.Error()
					return m.syncWindowTitleCmd()
				}
				m.closeModelDialog()
				m.status = "Selected model " + action.ModelID
				m.refreshViewport()
				return m.syncWindowTitleCmd()
			case dialogs.ModelDialogActionCancel:
				m.closeModelDialog()
				m.status = "Model selection cancelled"
				return m.syncWindowTitleCmd()
			default:
				return nil
			}
		}))
	}
	if m.hasDisconnectDialog() {
		windows = append(windows, m.centeredWindow(disconnectWindowID, 30, m.renderDisconnectDialogElement(), func(m *Model, msg tea.KeyMsg) tea.Cmd {
			_, cmd := m.handleDisconnectDialogKey(msg)
			return cmd
		}, func(m *Model, controlID string) tea.Cmd {
			action := m.disconnectDialog.ActivateControl(controlID)
			switch action.Kind {
			case dialogs.DisconnectDialogActionSelect:
				if err := m.disconnectProvider(action.ProviderID); err != nil {
					m.status = err.Error()
					return m.syncWindowTitleCmd()
				}
				m.closeDisconnectDialog()
				m.status = "Disconnected provider " + action.ProviderID
				m.refreshViewport()
				return m.syncWindowTitleCmd()
			case dialogs.DisconnectDialogActionCancel:
				m.closeDisconnectDialog()
				m.status = "Provider disconnect cancelled"
				return m.syncWindowTitleCmd()
			default:
				return nil
			}
		}))
	}
	if m.hasToolsDialog() {
		windows = append(windows, m.centeredWindow(toolsWindowID, 40, m.renderToolsDialogElement(), func(m *Model, msg tea.KeyMsg) tea.Cmd {
			_, cmd := m.handleToolsDialogKey(msg)
			return cmd
		}, func(m *Model, controlID string) tea.Cmd {
			action := m.toolsDialog.ActivateControl(controlID)
			switch action.Kind {
			case dialogs.ToolsDialogActionApply:
				if err := m.applySessionToolStates(action.States); err != nil {
					m.status = err.Error()
					return m.syncWindowTitleCmd()
				}
				m.closeToolsDialog()
				m.status = "Session tools updated"
				return m.syncWindowTitleCmd()
			case dialogs.ToolsDialogActionCancel:
				m.closeToolsDialog()
				m.status = "Tool selection cancelled"
				return m.syncWindowTitleCmd()
			default:
				return nil
			}
		}))
	}
	if m.hasConnectDialog() {
		windows = append(windows, m.centeredWindow(connectWindowID, 50, m.renderConnectDialogElement(), func(m *Model, msg tea.KeyMsg) tea.Cmd {
			_, cmd := m.handleConnectDialogKey(msg)
			return cmd
		}, func(m *Model, controlID string) tea.Cmd {
			if controlID == "window-close" {
				m.closeConnectDialog()
				m.status = "Provider connect cancelled"
				return m.syncWindowTitleCmd()
			}
			return nil
		}))
	}
	if m.hasAgentsModal() {
		windows = append(windows, m.centeredWindow(agentsWindowID, 60, m.renderAgentsModalElement(), func(m *Model, msg tea.KeyMsg) tea.Cmd {
			switch msg.String() {
			case "esc", "enter":
				m.closeAgentsModal()
				return m.syncWindowTitleCmd()
			default:
				return nil
			}
		}, func(m *Model, controlID string) tea.Cmd {
			if controlID == "window-close" {
				m.closeAgentsModal()
				return m.syncWindowTitleCmd()
			}
			return nil
		}))
	}
	if m.hasHelpModal() {
		windows = append(windows, m.centeredWindow(helpWindowID, 70, m.renderHelpModalElement(), func(m *Model, msg tea.KeyMsg) tea.Cmd {
			switch msg.String() {
			case "esc", "enter", "alt+h":
				m.closeHelpModal()
				return m.syncWindowTitleCmd()
			default:
				return nil
			}
		}, func(m *Model, controlID string) tea.Cmd {
			if controlID == "window-close" {
				m.closeHelpModal()
				return m.syncWindowTitleCmd()
			}
			return nil
		}))
	}
	if m.hasLLMPreview() {
		windows = append(windows, m.centeredWindow(llmPreviewWindowID, 80, m.renderLLMPreviewElement(), func(m *Model, msg tea.KeyMsg) tea.Cmd {
			switch msg.String() {
			case "esc", "enter", "alt+o":
				m.closeLLMPreview()
				return m.syncWindowTitleCmd()
			default:
				if m.handleLLMPreviewKey(msg) {
					return nil
				}
				return nil
			}
		}, func(m *Model, msgControl string) tea.Cmd {
			if msgControl == "window-close" {
				m.closeLLMPreview()
				return m.syncWindowTitleCmd()
			}
			return nil
		}))
	}
	if m.hasPreferencesDialog() {
		windows = append(windows, m.centeredWindow(preferencesWindowID, 90, m.renderPreferencesDialogElement(), func(m *Model, msg tea.KeyMsg) tea.Cmd {
			_, cmd := m.handlePreferencesKey(msg)
			return cmd
		}, func(m *Model, controlID string) tea.Cmd {
			if controlID == "window-close" {
				m.closePreferencesDialog()
				m.status = "Preferences cancelled"
				return m.syncWindowTitleCmd()
			}
			return nil
		}))
	}
	if m.hasPicker() {
		windows = append(windows, m.centeredWindow(pickerWindowID, 100, m.renderPickerElement(), func(m *Model, msg tea.KeyMsg) tea.Cmd {
			if msg.String() == "ctrl+c" {
				_, cmd := m.quit()
				return cmd
			}
			action := m.picker.dialog.Update(msg)
			m.previewSelectedTheme()
			switch action.Kind {
			case ui.PickerDialogActionSelect:
				_, cmd := m.submitPickerSelection(action.Value)
				return cmd
			case ui.PickerDialogActionCancel:
				_, cmd := m.cancelPicker()
				return cmd
			default:
				return nil
			}
		}, func(m *Model, controlID string) tea.Cmd {
			action := m.picker.dialog.ActivateControl(controlID)
			switch action.Kind {
			case ui.PickerDialogActionSelect:
				_, cmd := m.submitPickerSelection(action.Value)
				return cmd
			case ui.PickerDialogActionCancel:
				_, cmd := m.cancelPicker()
				return cmd
			default:
				m.previewSelectedTheme()
				return nil
			}
		}))
	}
	return windows
}

func (m *Model) centeredWindow(id ui.WindowID, z int, element ui.Element, onKey func(*Model, tea.KeyMsg) tea.Cmd, onControl func(*Model, string) tea.Cmd) ui.Window {
	return &modelWindow{
		base: ui.BaseWindow{
			WindowID:      id,
			Order:         z,
			FocusableFlag: true,
			VisibleFlag:   true,
			ModalFlag:     true,
			Dirty:         true,
		},
		model: m,
		bounds: func(m *Model, root ui.Rect) ui.Rect {
			if element == nil {
				return ui.Rect{}
			}
			ctx := &ui.Context{Palette: m.palette}
			size := m.centeredModal(element).Measure(ctx, ui.NewConstraints(root.W, root.H))
			return ui.Rect{
				X: max(0, (root.W-size.W)/2),
				Y: max(0, (root.H-size.H)/2),
				W: min(root.W, size.W),
				H: min(root.H, size.H),
			}
		},
		render: func(m *Model, bounds ui.Rect) ui.Surface {
			if element == nil {
				return ui.Surface{}
			}
			return element.Render(&ui.Context{Palette: m.palette}, ui.Rect{W: bounds.W, H: bounds.H}).Normalize(bounds.W, bounds.H)
		},
		key: onKey,
		mouse: func(m *Model, msg tea.MouseMsg) (bool, tea.Cmd) {
			if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
				if id == llmPreviewWindowID {
					if m.handleLLMPreviewMouse(msg) {
						return true, nil
					}
				}
				return true, nil
			}
			runtime := ui.Runtime{}
			ctx := &ui.Context{Palette: m.palette, Runtime: &runtime}
			bounds := m.centeredWindowBounds(element)
			element.Render(ctx, ui.Rect{W: bounds.W, H: bounds.H})
			local := ui.Point{X: msg.X - bounds.X, Y: msg.Y - bounds.Y}
			if control, ok := runtime.Hit(local); ok {
				return true, onControl(m, control.ID)
			}
			return true, nil
		},
	}
}

func (m *Model) centeredWindowBounds(element ui.Element) ui.Rect {
	if element == nil {
		return ui.Rect{}
	}
	ctx := &ui.Context{Palette: m.palette}
	root := ui.Rect{W: max(0, m.width), H: max(0, m.height)}
	size := m.centeredModal(element).Measure(ctx, ui.NewConstraints(root.W, root.H))
	return ui.Rect{
		X: max(0, (root.W-size.W)/2),
		Y: max(0, (root.H-size.H)/2),
		W: min(root.W, size.W),
		H: min(root.H, size.H),
	}
}
