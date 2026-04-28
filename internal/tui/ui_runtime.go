package tui

import (
	"fmt"

	"github.com/lkarlslund/koder/internal/tui/dialogs"
	"github.com/lkarlslund/koder/internal/ui"
)

const (
	mainWindowID        ui.WindowID = "main"
	sessionWindowID     ui.WindowID = "session-dialog"
	preferencesWindowID ui.WindowID = "preferences-dialog"
	toolsWindowID       ui.WindowID = "tools-dialog"
	connectWindowID     ui.WindowID = "connect-dialog"
	mcpWindowID         ui.WindowID = "mcp-dialog"
	mcpEditWindowID     ui.WindowID = "mcp-edit-dialog"
	disconnectWindowID  ui.WindowID = "disconnect-dialog"
	modelWindowID       ui.WindowID = "model-dialog"
	themeWindowID       ui.WindowID = "theme-dialog"
	agentsWindowID      ui.WindowID = "agents-modal"
	helpWindowID        ui.WindowID = "help-modal"
	llmPreviewWindowID  ui.WindowID = "llm-preview"
	pickerWindowID      ui.WindowID = "picker-dialog"
	approvalWindowID    ui.WindowID = "approval-dialog"
)

type modelWindow struct {
	base        ui.BaseWindow
	model       *Model
	bounds      func(*Model, ui.Rect) ui.Rect
	element     func(*Model) ui.Node
	render      func(*Model, ui.Rect) ui.Surface
	paint       func(*Model, *ui.Context, ui.Rect, *ui.Surface) []ui.Rect
	frameDirty  []ui.Rect
	invalidate  func(*Model, *ui.Context)
	needsRedraw func(*Model) bool
	dirtyRects  func(*Model) []ui.Rect
	key         func(*Model, ui.KeyMsg) (bool, ui.Cmd)
	mouse       func(*Model, ui.MouseMsg) (bool, ui.Cmd)
	timer       func(*Model, ui.TimerEvent) (bool, ui.Cmd)
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
	if w != nil && w.needsRedraw != nil && w.needsRedraw(w.model) {
		return true
	}
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

func (w *modelWindow) HandleKey(msg ui.KeyMsg) (bool, ui.Cmd) {
	if w.key == nil {
		return false, nil
	}
	return w.key(w.model, msg)
}

func (w *modelWindow) HandleMouse(msg ui.MouseMsg) (bool, ui.Cmd) {
	if w.mouse == nil {
		return false, nil
	}
	return w.mouse(w.model, msg)
}

func (w *modelWindow) PaintWindow(ctx *ui.Context, bounds ui.Rect, dst *ui.Surface) {
	if w == nil || dst == nil {
		if w != nil {
			w.frameDirty = nil
		}
		return
	}
	if w.paint == nil {
		if w.render == nil {
			w.frameDirty = nil
			return
		}
		surface := w.render(w.model, bounds).Normalize(bounds.W, bounds.H)
		*dst = dst.PlaceAt(bounds.X, bounds.Y, surface)
		if rects, ok := surface.DirtyRects(); ok {
			w.frameDirty = append(w.frameDirty[:0], rects...)
		} else if bounds.W > 0 && bounds.H > 0 {
			w.frameDirty = []ui.Rect{{W: bounds.W, H: bounds.H}}
		} else {
			w.frameDirty = nil
		}
		return
	}
	w.frameDirty = w.paint(w.model, ctx, bounds, dst)
}

func (w *modelWindow) WindowDirtyRects() []ui.Rect {
	if w != nil && len(w.frameDirty) > 0 {
		return append([]ui.Rect(nil), w.frameDirty...)
	}
	if w == nil || w.dirtyRects == nil {
		return nil
	}
	return w.dirtyRects(w.model)
}

func (w *modelWindow) CanPaintWindow() bool {
	return w != nil && (w.paint != nil || w.render != nil)
}

func (w *modelWindow) InvalidateCaches(ctx *ui.Context) {
	if w == nil {
		return
	}
	if w.invalidate != nil {
		w.invalidate(w.model, ctx)
		w.base.Dirty = true
		return
	}
	if w.element != nil {
		ui.InvalidateNodeCaches(ctx, w.element(w.model))
	}
	w.base.Dirty = true
}

func (w *modelWindow) HandleTimer(event ui.TimerEvent) (bool, ui.Cmd) {
	if w.timer == nil {
		return false, nil
	}
	return w.timer(w.model, event)
}

func (m *Model) ensureUIRoot() *ui.Root {
	if m.uiRoot == nil {
		m.uiRoot = ui.NewRoot(m.palette, ui.Rect{W: max(0, m.width), H: max(0, m.height)})
	}
	return m.uiRoot
}

func (m *Model) syncUIRoot() *ui.Root {
	root := m.ensureUIRoot()
	root.SetPalette(m.palette)
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
	if m.mainWindowView == nil {
		m.mainWindowView = &modelWindow{
			base: ui.BaseWindow{
				WindowID:      mainWindowID,
				Order:         0,
				FocusableFlag: true,
				VisibleFlag:   true,
				Dirty:         true,
				OnFocus: func() {
					m.syncComposerVisibility()
				},
				OnBlur: func() {
					m.syncComposerVisibility()
				},
			},
			model: m,
			element: func(m *Model) ui.Node {
				return m.renderBodyElement()
			},
			render: func(m *Model, bounds ui.Rect) ui.Surface {
				return m.renderBodySurface().Normalize(max(0, bounds.W), max(0, bounds.H))
			},
			invalidate: func(m *Model, ctx *ui.Context) {
				ui.InvalidateNodeCaches(ctx, m.renderBodyElement())
				m.invalidateBodyCache()
			},
			needsRedraw: func(m *Model) bool {
				if m == nil || m.mainScreen == nil {
					return false
				}
				return m.mainScreen.dirty()
			},
			key: func(m *Model, msg ui.KeyMsg) (bool, ui.Cmd) {
				return m.handleMainWindowKey(msg)
			},
			mouse: func(m *Model, msg ui.MouseMsg) (bool, ui.Cmd) {
				return m.handleMainWindowMouse(msg)
			},
			timer: func(m *Model, event ui.TimerEvent) (bool, ui.Cmd) {
				if event.Owner != composerBlinkTimerOwner {
					return false, nil
				}
				if !m.composer.ToggleBlink() {
					return false, nil
				}
				m.invalidateFooterCursor()
				return true, nil
			},
		}
		return m.mainWindowView
	}
	m.mainWindowView.model = m
	return m.mainWindowView
}

func (m *Model) overlayWindows() []ui.Window {
	windows := make([]ui.Window, 0, 8)
	if m.hasSessionDialog() {
		windows = append(windows, m.centeredWindow(sessionWindowID, 10, m.renderSessionDialogElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			return m.handleSessionDialogKey(msg)
		}, func(m *Model, controlID string) ui.Cmd {
			if controlID == "window-close" {
				m.startBusy(busyScopeSidebar, "Creating session…")
				return ui.Batch(m.newSessionCmd(), m.spinnerCmdIfNeeded())
			}
			action := m.sessionDialog.ActivateControl(controlID)
			switch action.Kind {
			case dialogs.SessionDialogActionSelect:
				m.startBusy(busyScopeSidebar, "Resuming session…")
				return ui.Batch(m.loadSessionCmd(action.SessionID), m.spinnerCmdIfNeeded())
			case dialogs.SessionDialogActionCancel:
				m.startBusy(busyScopeSidebar, "Creating session…")
				return ui.Batch(m.newSessionCmd(), m.spinnerCmdIfNeeded())
			default:
				return nil
			}
		}))
	}
	if m.hasModelDialog() {
		windows = append(windows, m.centeredWindow(modelWindowID, 20, m.renderModelDialogElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			return m.handleModelDialogKey(msg)
		}, func(m *Model, controlID string) ui.Cmd {
			if controlID == "window-close" {
				m.closeModelDialog()
				m.status = "Model selection cancelled"
				return m.syncWindowTitleCmd()
			}
			action := m.modelDialog.ActivateControl(controlID)
			switch action.Kind {
			case dialogs.ModelDialogActionSelect:
				if err := m.selectModel(action.ProviderID, action.ModelID, action.PresetID); err != nil {
					m.status = err.Error()
					return m.syncWindowTitleCmd()
				}
				m.closeModelDialog()
				m.status = "Selected " + action.ProviderID + " / " + action.ModelID
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
		windows = append(windows, m.centeredWindow(disconnectWindowID, 30, m.renderDisconnectDialogElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			return m.handleDisconnectDialogKey(msg)
		}, func(m *Model, controlID string) ui.Cmd {
			if controlID == "window-close" {
				m.closeDisconnectDialog()
				m.status = "Provider disconnect cancelled"
				return m.syncWindowTitleCmd()
			}
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
		windows = append(windows, m.centeredWindow(toolsWindowID, 40, m.renderToolsDialogElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			return m.handleToolsDialogKey(msg)
		}, func(m *Model, controlID string) ui.Cmd {
			if controlID == "window-close" {
				m.closeToolsDialog()
				m.status = "Tool selection cancelled"
				return m.syncWindowTitleCmd()
			}
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
		windows = append(windows, m.centeredWindow(connectWindowID, 50, m.renderConnectDialogElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			return m.handleConnectDialogKey(msg)
		}, func(m *Model, controlID string) ui.Cmd {
			if controlID == "window-close" {
				m.closeConnectDialog()
				m.status = "Provider connect cancelled"
				return m.syncWindowTitleCmd()
			}
			action := m.connectDialog.ActivateControl(controlID)
			switch action.Kind {
			case dialogs.ProviderConnectActionTest:
				m.connectDialog.SetStatus("Testing connection…")
				return ui.Batch(m.probeProviderCmd(action.Draft), m.syncWindowTitleCmd())
			case dialogs.ProviderConnectActionSave:
				if err := m.saveProviderDraft(action.Draft); err != nil {
					m.connectDialog.SetStatusError("Save failed: " + err.Error())
					m.status = err.Error()
					return m.syncWindowTitleCmd()
				}
				m.closeConnectDialog()
				m.status = fmt.Sprintf("Connected provider %s", action.Draft.ProviderID)
				return ui.Batch(m.loadModelsCmd(action.Draft.ProviderID, true), m.syncWindowTitleCmd())
			case dialogs.ProviderConnectActionCancel:
				m.closeConnectDialog()
				m.status = "Provider connect cancelled"
				return m.syncWindowTitleCmd()
			default:
				return nil
			}
		}))
	}
	if m.hasMCPDialog() {
		windows = append(windows, m.centeredWindow(mcpWindowID, 55, m.renderMCPDialogElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			return m.handleMCPDialogKey(msg)
		}, func(m *Model, controlID string) ui.Cmd {
			return m.applyMCPDialogAction(m.mcpDialog.ActivateListControl(controlID), false)
		}))
	}
	if m.hasMCPEditDialog() {
		windows = append(windows, m.centeredWindow(mcpEditWindowID, 56, m.renderMCPEditDialogElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			return m.handleMCPEditDialogKey(msg)
		}, func(m *Model, controlID string) ui.Cmd {
			return m.applyMCPDialogAction(m.mcpDialog.ActivateEditorControl(controlID), true)
		}))
	}
	if m.hasAgentsModal() {
		windows = append(windows, m.centeredWindow(agentsWindowID, 60, m.renderAgentsModalElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			switch msg.String() {
			case "esc", "enter":
				m.closeAgentsModal()
				return m.syncWindowTitleCmd()
			default:
				return nil
			}
		}, func(m *Model, controlID string) ui.Cmd {
			if controlID == "window-close" {
				m.closeAgentsModal()
				return m.syncWindowTitleCmd()
			}
			return nil
		}))
	}
	if m.hasHelpModal() {
		windows = append(windows, m.centeredWindow(helpWindowID, 70, m.renderHelpModalElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			switch msg.String() {
			case "esc", "enter", "alt+h":
				m.closeHelpModal()
				return m.syncWindowTitleCmd()
			default:
				return nil
			}
		}, func(m *Model, controlID string) ui.Cmd {
			if controlID == "window-close" {
				m.closeHelpModal()
				return m.syncWindowTitleCmd()
			}
			return nil
		}))
	}
	if m.hasLLMPreview() {
		windows = append(windows, m.centeredWindow(llmPreviewWindowID, 80, m.renderLLMPreviewElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
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
		}, func(m *Model, msgControl string) ui.Cmd {
			if msgControl == "window-close" {
				m.closeLLMPreview()
				return m.syncWindowTitleCmd()
			}
			return nil
		}))
	}
	if m.hasPreferencesDialog() {
		windows = append(windows, m.centeredWindow(preferencesWindowID, 90, m.renderPreferencesDialogElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			return m.handlePreferencesKey(msg)
		}, func(m *Model, controlID string) ui.Cmd {
			if controlID == "window-close" {
				m.closePreferencesDialog()
				m.status = "Preferences cancelled"
				return m.syncWindowTitleCmd()
			}
			return nil
		}))
	}
	if m.hasThemeDialog() {
		windows = append(windows, m.centeredWindow(themeWindowID, 100, m.renderThemeDialogElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			if msg.String() == "ctrl+c" {
				_, cmd := m.quit()
				return cmd
			}
			action := m.themeDialog.Update(msg)
			m.previewSelectedTheme()
			switch action.Kind {
			case dialogs.ThemeDialogActionSelect:
				_, cmd := m.submitThemeSelection(action.Theme)
				return cmd
			case dialogs.ThemeDialogActionCancel:
				_, cmd := m.cancelThemeDialog()
				return cmd
			default:
				return nil
			}
		}, func(m *Model, controlID string) ui.Cmd {
			if controlID == "window-close" {
				_, cmd := m.cancelThemeDialog()
				return cmd
			}
			action := m.themeDialog.ActivateControl(controlID)
			switch action.Kind {
			case dialogs.ThemeDialogActionSelect:
				_, cmd := m.submitThemeSelection(action.Theme)
				return cmd
			case dialogs.ThemeDialogActionCancel:
				_, cmd := m.cancelThemeDialog()
				return cmd
			default:
				m.previewSelectedTheme()
				return nil
			}
		}))
	}
	if m.hasApprovalDialog() {
		windows = append(windows, m.centeredWindow(approvalWindowID, 90, m.renderApprovalDialogElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			action := m.approvalDialog.Update(msg)
			return m.handleApprovalDialogAction(action)
		}, func(m *Model, controlID string) ui.Cmd {
			action := m.approvalDialog.ActivateControl(controlID)
			return m.handleApprovalDialogAction(action)
		}))
	}
	if m.hasPicker() {
		windows = append(windows, m.centeredWindow(pickerWindowID, 100, m.renderPickerElement(), func(m *Model, msg ui.KeyMsg) ui.Cmd {
			if msg.String() == "ctrl+c" {
				_, cmd := m.quit()
				return cmd
			}
			action := m.picker.dialog.Update(msg)
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
		}, func(m *Model, controlID string) ui.Cmd {
			action := m.picker.dialog.ActivateControl(controlID)
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
		}))
	}
	return windows
}

func (m *Model) centeredWindow(id ui.WindowID, z int, element ui.Node, onKey func(*Model, ui.KeyMsg) ui.Cmd, onControl func(*Model, string) ui.Cmd) ui.Window {
	return &modelWindow{
		base: ui.BaseWindow{
			WindowID:      id,
			Order:         z,
			FocusableFlag: true,
			VisibleFlag:   true,
			ModalFlag:     true,
			Dirty:         true,
		},
		model:   m,
		element: func(*Model) ui.Node { return element },
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
			return ui.PaintNodeSurface(&ui.Context{Palette: m.palette}, element, ui.Rect{W: bounds.W, H: bounds.H}).Normalize(bounds.W, bounds.H)
		},
		key: func(m *Model, msg ui.KeyMsg) (bool, ui.Cmd) {
			return true, onKey(m, msg)
		},
		mouse: func(m *Model, msg ui.MouseMsg) (bool, ui.Cmd) {
			if msg.Action != ui.MouseActionPress || msg.Button != ui.MouseButtonLeft {
				if id == llmPreviewWindowID {
					if m.handleLLMPreviewMouse(msg) {
						return true, nil
					}
				}
				return true, nil
			}
			bounds := m.centeredWindowBounds(element)
			local := ui.Point{X: max(0, msg.X-1-bounds.X), Y: msg.Y - bounds.Y}
			surface := ui.PaintNodeSurface(&ui.Context{Palette: m.palette}, element, ui.Rect{W: bounds.W, H: bounds.H})
			controls := surface.Controls()
			for idx := len(controls) - 1; idx >= 0; idx-- {
				control := controls[idx]
				if control.Enabled && control.Rect.Contains(local) {
					return true, onControl(m, control.ID)
				}
			}
			return true, nil
		},
	}
}

func (m *Model) centeredWindowBounds(element ui.Node) ui.Rect {
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
