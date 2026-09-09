package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"log"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

const configFile = "joysettings.json"

type ControlMapping struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "button" or "axis"
	ID       int    `json:"id"`
	IsSlider bool   `json:"is_slider"`
}

type JoySettings struct {
	DeviceType  string           `json:"device_type"` // "radiomaster" or "other"
	Mode        string           `json:"mode"`        // "Mode1" or "Mode2"
	Mappings    []ControlMapping `json:"mappings"`
	SliderAxis  int              `json:"slider_axis"`  // -1 if unused
	SliderMin   float32          `json:"slider_min"`
	SliderMax   float32          `json:"slider_max"`
	Slider2Axis int              `json:"slider2_axis"` // -1 if unused
	Slider2Min  float32          `json:"slider2_min"`
	Slider2Max  float32          `json:"slider2_max"`
}

type WizardStep int

const (
	StepNone WizardStep = iota
	StepDeviceType
	StepSelectMode
	StepRMWaitSwitches
	StepRMS1Axis
	StepRMS1Min
	StepRMS1Max
	StepOtherNumButtons
	StepOtherHasSlider
	StepOtherWaitButtonPress
	StepOtherWaitButtonLabel
	StepOtherSlider1Axis
	StepOtherSlider1Min
	StepOtherSlider1Max
	StepOtherHasSecondSlider
	StepOtherSlider2Axis
	StepOtherSlider2Min
	StepOtherSlider2Max
)

type Game struct {
	inputText   string
	outputLines []string
	runes       []rune

	configLoaded bool
	settings     JoySettings

	// Wizard State
	wizardStep      WizardStep
	rmPromptIndex   int
	rmPrompts       []string
	axisBaselines   map[int]float32
	otherBtnCount   int
	otherBtnIndex   int
	otherHasSlider  bool
	tempPendingBtn  int
	tempPendingType string
}

func NewGame() *Game {
	g := &Game{
		axisBaselines: make(map[int]float32),
		settings: JoySettings{
			Mode:        "Mode1",
			SliderAxis:  -1,
			Slider2Axis: -1,
			Mappings:    []ControlMapping{},
		},
		rmPrompts: []string{"SA", "SB", "SC", "SD", "SE"},
		outputLines: []string{
			"System Console Initialized.",
			"Type 'help' for a list of available commands.",
		},
	}

	if _, err := os.Stat(configFile); err == nil {
		if err := g.loadConfigFromFile(); err == nil {
			g.configLoaded = true
			g.outputLines = append(g.outputLines, "Existing config auto-loaded from joysettings.json.")
		}
	}

	return g
}

func (g *Game) Update() error {
	g.runes = ebiten.AppendInputChars(g.runes[:0])
	for _, r := range g.runes {
		if r >= 32 && r <= 126 {
			g.inputText += string(r)
		}
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) && len(g.inputText) > 0 {
		g.inputText = g.inputText[:len(g.inputText)-1]
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) {
		cmd := strings.TrimSpace(g.inputText)
		g.inputText = ""

		if g.wizardStep != StepNone {
			g.processWizardText(cmd)
		} else if len(cmd) > 0 {
			g.outputLines = append(g.outputLines, "> "+cmd)
			g.processCommand(cmd)
		}
	}

	if g.wizardStep != StepNone {
		g.processWizardPhysicalInput()
	}

	if len(g.outputLines) > 10 {
		g.outputLines = g.outputLines[len(g.outputLines)-10:]
	}

	return nil
}

func (g *Game) processCommand(cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return
	}

	switch strings.ToLower(parts[0]) {
	case "help":
		g.outputLines = append(g.outputLines,
			" Available commands:",
			"   config / create - Run interactive hardware setup wizard",
			"   load            - Reload configuration from joysettings.json",
			"   mode <name>     - Set active mode (Mode1/Mode2)",
			"   clear           - Clear terminal logs",
		)

	case "load":
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			g.configLoaded = false
			g.outputLines = append(g.outputLines, "ERROR: joysettings.json does not exist. Run 'create' or 'config' first.")
		} else if err := g.loadConfigFromFile(); err == nil {
			g.configLoaded = true
			g.outputLines = append(g.outputLines, "Config: loaded successfully from joysettings.json")
		} else {
			g.configLoaded = false
			g.outputLines = append(g.outputLines, "ERROR reading joysettings.json file.")
		}

	case "config", "create":
		g.startWizard()

	case "mode":
		if len(parts) > 1 {
			targetMode := strings.ToUpper(parts[1])
			if targetMode == "MODE1" || targetMode == "MODE2" {
				if targetMode == "MODE1" {
					g.settings.Mode = "Mode1"
				} else {
					g.settings.Mode = "Mode2"
				}
				g.saveConfigToFile()
				g.outputLines = append(g.outputLines, fmt.Sprintf("Mode changed to: %s", g.settings.Mode))
			} else {
				g.outputLines = append(g.outputLines, "Invalid mode. Use 'mode Mode1' or 'mode Mode2'")
			}
		} else {
			g.outputLines = append(g.outputLines, fmt.Sprintf("Current mode: %s", g.settings.Mode))
		}

	case "clear":
		g.outputLines = []string{"Console output cleared."}

	default:
		g.outputLines = append(g.outputLines, fmt.Sprintf("Unknown command: '%s'. Type 'help'.", parts[0]))
	}
}

func (g *Game) startWizard() {
	ids := ebiten.GamepadIDs()
	if len(ids) == 0 {
		g.outputLines = append(g.outputLines, "ERROR: No controller detected. Plug in controller before configuring.")
		return
	}

	if g.axisBaselines == nil {
		g.axisBaselines = make(map[int]float32)
	}

	g.wizardStep = StepDeviceType
	g.settings.Mappings = []ControlMapping{}
	g.settings.SliderAxis = -1
	g.settings.Slider2Axis = -1
	g.outputLines = append(g.outputLines,
		"--- HARDWARE SETUP WIZARD ---",
		"Is this device: 1) RadioMaster Pocket  2) Other? (Type 1 or 2)",
	)
}

func (g *Game) processWizardText(input string) {
	ids := ebiten.GamepadIDs()
	maxAxis := 7
	if len(ids) > 0 {
		maxAxis = ebiten.GamepadAxisCount(ids[0]) - 1
	}

	switch g.wizardStep {
	case StepDeviceType:
		if input == "1" {
			g.settings.DeviceType = "radiomaster"
			g.wizardStep = StepSelectMode
			g.outputLines = append(g.outputLines, "Selected: RadioMaster Pocket", "Select Mode: 1) Mode1  2) Mode2 (Type 1 or 2)")
		} else if input == "2" {
			g.settings.DeviceType = "other"
			g.wizardStep = StepSelectMode
			g.outputLines = append(g.outputLines, "Selected: Generic / Other", "Select Mode: 1) Mode1  2) Mode2 (Type 1 or 2)")
		} else {
			g.outputLines = append(g.outputLines, "Invalid choice. Enter 1 for RadioMaster or 2 for Other.")
		}

	case StepSelectMode:
		if input == "1" {
			g.settings.Mode = "Mode1"
		} else {
			g.settings.Mode = "Mode2"
		}

		if g.settings.DeviceType == "radiomaster" {
			g.wizardStep = StepRMWaitSwitches
			g.rmPromptIndex = 0
			g.snapshotBaselines()
			g.outputLines = append(g.outputLines, fmt.Sprintf("Mode set to %s.", g.settings.Mode), "-> Press "+g.rmPrompts[0])
		} else {
			g.wizardStep = StepOtherNumButtons
			g.outputLines = append(g.outputLines, fmt.Sprintf("Mode set to %s.", g.settings.Mode), "How many buttons do you have? (0-16):")
		}

	case StepRMS1Axis:
		axisIdx, err := strconv.Atoi(input)
		if err != nil || axisIdx < 0 || axisIdx > maxAxis {
			g.outputLines = append(g.outputLines, fmt.Sprintf("Invalid axis. Enter an axis index between 0 and %d:", maxAxis))
			return
		}

		g.settings.SliderAxis = axisIdx
		g.wizardStep = StepRMS1Min
		g.outputLines = append(g.outputLines,
			fmt.Sprintf("S1 assigned to Axis %d.", axisIdx),
			"-> Turn S1 dial to MINIMUM, then type 'ok' and press Enter.",
		)

	case StepRMS1Min:
		if len(ids) == 0 {
			g.outputLines = append(g.outputLines, "ERROR: Controller disconnected.")
			return
		}
		id := ids[0]
		g.settings.SliderMin = float32(ebiten.GamepadAxisValue(id, g.settings.SliderAxis))
		g.wizardStep = StepRMS1Max

		g.outputLines = append(g.outputLines,
			fmt.Sprintf("S1 Min set to %0.2f", g.settings.SliderMin),
			"-> Turn S1 dial to MAXIMUM, then type 'ok' and press Enter.",
		)

	case StepRMS1Max:
		if len(ids) > 0 && g.settings.SliderAxis >= 0 {
			id := ids[0]
			g.settings.SliderMax = float32(ebiten.GamepadAxisValue(id, g.settings.SliderAxis))
			g.outputLines = append(g.outputLines, fmt.Sprintf("S1 Max set to %0.2f", g.settings.SliderMax))
			g.finishWizard()
		}

	case StepOtherNumButtons:
		num, err := strconv.Atoi(input)
		if err != nil || num < 0 || num > 16 {
			g.outputLines = append(g.outputLines, "Invalid number. Enter a count between 0 and 16:")
			return
		}
		g.otherBtnCount = num
		if g.otherBtnCount > 0 {
			g.wizardStep = StepOtherWaitButtonPress
			g.otherBtnIndex = 0
			g.snapshotBaselines()
			g.outputLines = append(g.outputLines, fmt.Sprintf("-> Press Button %d on controller...", g.otherBtnIndex+1))
		} else {
			g.wizardStep = StepOtherHasSlider
			g.outputLines = append(g.outputLines, "Do you have a slider/dial? (y/n):")
		}

	case StepOtherHasSlider:
		if strings.ToLower(input) == "y" {
			g.wizardStep = StepOtherSlider1Axis
			g.outputLines = append(g.outputLines, fmt.Sprintf("Select Slider 1 axis index manually (0 to %d):", maxAxis))
		} else {
			g.finishWizard()
		}

	case StepOtherWaitButtonLabel:
		label := strings.TrimSpace(input)
		if len(label) > 4 {
			label = label[:4]
		}
		if len(label) == 0 {
			label = fmt.Sprintf("B%d", g.otherBtnIndex+1)
		}

		g.settings.Mappings = append(g.settings.Mappings, ControlMapping{
			Name: label,
			Type: g.tempPendingType,
			ID:   g.tempPendingBtn,
		})

		g.otherBtnIndex++
		if g.otherBtnIndex < g.otherBtnCount {
			g.wizardStep = StepOtherWaitButtonPress
			g.snapshotBaselines()
			g.outputLines = append(g.outputLines, fmt.Sprintf("-> Press Button %d on controller...", g.otherBtnIndex+1))
		} else {
			g.wizardStep = StepOtherHasSlider
			g.outputLines = append(g.outputLines, "Do you have a slider/dial? (y/n):")
		}

	case StepOtherSlider1Axis:
		axisIdx, err := strconv.Atoi(input)
		if err != nil || axisIdx < 0 || axisIdx > maxAxis {
			g.outputLines = append(g.outputLines, fmt.Sprintf("Invalid axis. Enter an axis index between 0 and %d:", maxAxis))
			return
		}
		g.settings.SliderAxis = axisIdx
		g.wizardStep = StepOtherSlider1Min
		g.outputLines = append(g.outputLines,
			fmt.Sprintf("Slider 1 assigned to Axis %d.", axisIdx),
			"-> Set Slider 1 to MINIMUM position, type 'ok' and press Enter.",
		)

	case StepOtherSlider1Min:
		if len(ids) == 0 {
			g.outputLines = append(g.outputLines, "ERROR: Controller disconnected.")
			return
		}
		id := ids[0]
		g.settings.SliderMin = float32(ebiten.GamepadAxisValue(id, g.settings.SliderAxis))
		g.wizardStep = StepOtherSlider1Max
		g.outputLines = append(g.outputLines,
			fmt.Sprintf("Slider 1 Min set to %0.2f", g.settings.SliderMin),
			"-> Set Slider 1 to MAXIMUM position, type 'ok' and press Enter.",
		)

	case StepOtherSlider1Max:
		if len(ids) > 0 && g.settings.SliderAxis >= 0 {
			id := ids[0]
			g.settings.SliderMax = float32(ebiten.GamepadAxisValue(id, g.settings.SliderAxis))
			g.wizardStep = StepOtherHasSecondSlider
			g.outputLines = append(g.outputLines,
				fmt.Sprintf("Slider 1 Max set to %0.2f", g.settings.SliderMax),
				"Do you have a second slider/dial? (y/n):",
			)
		}

	case StepOtherHasSecondSlider:
		if strings.ToLower(input) == "y" {
			g.wizardStep = StepOtherSlider2Axis
			g.outputLines = append(g.outputLines, fmt.Sprintf("Select Slider 2 axis index manually (0 to %d):", maxAxis))
		} else {
			g.finishWizard()
		}

	case StepOtherSlider2Axis:
		axisIdx, err := strconv.Atoi(input)
		if err != nil || axisIdx < 0 || axisIdx > maxAxis || axisIdx == g.settings.SliderAxis {
			g.outputLines = append(g.outputLines, fmt.Sprintf("Invalid axis or already used for Slider 1. Enter an unused axis index (0 to %d):", maxAxis))
			return
		}
		g.settings.Slider2Axis = axisIdx
		g.wizardStep = StepOtherSlider2Min
		g.outputLines = append(g.outputLines,
			fmt.Sprintf("Slider 2 assigned to Axis %d.", axisIdx),
			"-> Set Slider 2 to MINIMUM position, type 'ok' and press Enter.",
		)

	case StepOtherSlider2Min:
		if len(ids) == 0 {
			g.outputLines = append(g.outputLines, "ERROR: Controller disconnected.")
			return
		}
		id := ids[0]
		g.settings.Slider2Min = float32(ebiten.GamepadAxisValue(id, g.settings.Slider2Axis))
		g.wizardStep = StepOtherSlider2Max
		g.outputLines = append(g.outputLines,
			fmt.Sprintf("Slider 2 Min set to %0.2f", g.settings.Slider2Min),
			"-> Set Slider 2 to MAXIMUM position, type 'ok' and press Enter.",
		)

	case StepOtherSlider2Max:
		if len(ids) > 0 && g.settings.Slider2Axis >= 0 {
			id := ids[0]
			g.settings.Slider2Max = float32(ebiten.GamepadAxisValue(id, g.settings.Slider2Axis))
			g.finishWizard()
		}
	}
}

func (g *Game) processWizardPhysicalInput() {
	ids := ebiten.GamepadIDs()
	if len(ids) == 0 {
		return
	}
	id := ids[0]

	switch g.wizardStep {
	case StepRMWaitSwitches:
		target := g.rmPrompts[g.rmPromptIndex]

		btnIdx, detectedBtn := g.detectButtonPress(id)
		if detectedBtn {
			g.settings.Mappings = append(g.settings.Mappings, ControlMapping{
				Name: target,
				Type: "button",
				ID:   btnIdx,
			})
			g.outputLines = append(g.outputLines, fmt.Sprintf("Mapped %s -> Btn %d", target, btnIdx))
			g.advanceRMPrompt()
			return
		}

		axisIdx, _, detectedAxis := g.detectAxisChange(id)
		if detectedAxis {
			g.settings.Mappings = append(g.settings.Mappings, ControlMapping{
				Name: target,
				Type: "axis",
				ID:   axisIdx,
			})
			g.outputLines = append(g.outputLines, fmt.Sprintf("Mapped %s -> Axis %d", target, axisIdx))
			g.advanceRMPrompt()
			return
		}

	case StepOtherWaitButtonPress:
		btnIdx, detectedBtn := g.detectButtonPress(id)
		if detectedBtn {
			g.tempPendingBtn = btnIdx
			g.tempPendingType = "button"
			g.wizardStep = StepOtherWaitButtonLabel
			g.outputLines = append(g.outputLines, fmt.Sprintf("Detected Btn %d. Enter label (max 4 chars):", btnIdx))
			return
		}

		axisIdx, _, detectedAxis := g.detectAxisChange(id)
		if detectedAxis {
			g.tempPendingBtn = axisIdx
			g.tempPendingType = "axis"
			g.wizardStep = StepOtherWaitButtonLabel
			g.outputLines = append(g.outputLines, fmt.Sprintf("Detected Axis %d. Enter label (max 4 chars):", axisIdx))
			return
		}
	}
}

func (g *Game) advanceRMPrompt() {
	g.rmPromptIndex++
	if g.rmPromptIndex < len(g.rmPrompts) {
		g.snapshotBaselines()
		g.outputLines = append(g.outputLines, "-> Press "+g.rmPrompts[g.rmPromptIndex])
	} else {
		g.wizardStep = StepRMS1Axis
		g.snapshotBaselines()
		g.outputLines = append(g.outputLines,
			"Switches SA-SE mapped successfully!",
			"Select S1 axis index manually (e.g. 4, 5, 6, 7):",
		)
	}
}

func (g *Game) finishWizard() {
	g.wizardStep = StepNone
	g.configLoaded = true
	g.saveConfigToFile()
	g.outputLines = append(g.outputLines, "WIZARD COMPLETE! Saved to joysettings.json.", "Config: loaded")
}

func (g *Game) snapshotBaselines() {
	if g.axisBaselines == nil {
		g.axisBaselines = make(map[int]float32)
	}
	ids := ebiten.GamepadIDs()
	if len(ids) == 0 {
		return
	}
	id := ids[0]
	count := ebiten.GamepadAxisCount(id)
	for i := 0; i < count; i++ {
		g.axisBaselines[i] = float32(ebiten.GamepadAxisValue(id, i))
	}
}

func (g *Game) detectButtonPress(id ebiten.GamepadID) (int, bool) {
	count := ebiten.GamepadButtonCount(id)
	for b := 0; b < count; b++ {
		if inpututil.IsGamepadButtonJustPressed(id, ebiten.GamepadButton(b)) {
			return b, true
		}
	}
	return -1, false
}

func (g *Game) detectAxisChange(id ebiten.GamepadID) (int, float32, bool) {
	if g.axisBaselines == nil {
		g.axisBaselines = make(map[int]float32)
	}
	count := ebiten.GamepadAxisCount(id)
	for i := 0; i < count; i++ {
		val := float32(ebiten.GamepadAxisValue(id, i))
		base := g.axisBaselines[i]
		if math.Abs(float64(val-base)) > 0.45 {
			return i, val, true
		}
	}
	return -1, 0, false
}

func (g *Game) saveConfigToFile() {
	data, err := json.MarshalIndent(g.settings, "", "  ")
	if err != nil {
		log.Println("Error encoding JSON config:", err)
		return
	}
	_ = os.WriteFile(configFile, data, 0644)
}

func (g *Game) loadConfigFromFile() error {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}
	var loaded JoySettings
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	g.settings = loaded
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{18, 18, 24, 255})

	ids := ebiten.GamepadIDs()
	if len(ids) == 0 {
		ebitenutil.DebugPrintAt(screen, "NO CONTROLLER DETECTED\n\nPlug in controller via USB/Bluetooth in Joystick Mode.", 40, 40)
		return
	}

	id := ids[0]
	name := ebiten.GamepadName(id)
	axisCount := ebiten.GamepadAxisCount(id)
	buttonCount := ebiten.GamepadButtonCount(id)

	header := fmt.Sprintf("Device: %s | Active Axes: %d | Active Buttons: %d", name, axisCount, buttonCount)
	ebitenutil.DebugPrintAt(screen, header, 20, 10)

	getAxis := func(idx int) float32 {
		if idx < axisCount {
			return float32(ebiten.GamepadAxisValue(id, idx))
		}
		return 0
	}

	// -------------------------------------------------------------------------
	// Top Left: Dynamic Analog Axes List
	// -------------------------------------------------------------------------
	ebitenutil.DebugPrintAt(screen, "ANALOG AXES", 20, 30)
	renderY := 0
	for i := 0; i < axisCount; i++ {
		if g.configLoaded && (g.settings.SliderAxis == i || g.settings.Slider2Axis == i) {
			continue
		}

		val := getAxis(i)
		y := float32(46 + renderY*21)

		axisLabel := fmt.Sprintf("Axis %d", i)
		if i < 4 {
			axisLabel = fmt.Sprintf("Axis %d-CH%d", i, i+1)
		}
		ebitenutil.DebugPrintAt(screen, fmt.Sprintf("%s (%+0.2f)", axisLabel, val), 20, int(y))

		barX := float32(150)
		barWidth := float32(160)
		vector.DrawFilledRect(screen, barX, y+2, barWidth, 10, color.RGBA{40, 44, 52, 255}, false)
		vector.DrawFilledRect(screen, barX+barWidth/2-1, y, 2, 14, color.RGBA{120, 120, 120, 255}, false)

		absVal := float32(math.Abs(float64(val)))
		greenComp := uint8(50 + absVal*205)
		blueComp := uint8(20 + absVal*90)
		barColor := color.RGBA{0, greenComp, blueComp, 255}

		fillWidth := val * (barWidth / 2)
		if fillWidth >= 0 {
			vector.DrawFilledRect(screen, barX+barWidth/2, y+2, fillWidth, 10, barColor, false)
		} else {
			vector.DrawFilledRect(screen, barX+barWidth/2+fillWidth, y+2, -fillWidth, 10, barColor, false)
		}
		renderY++
	}

	// -------------------------------------------------------------------------
	// Top Right: Digital Buttons List
	// -------------------------------------------------------------------------
	startX := float32(360)
	ebitenutil.DebugPrintAt(screen, "DIGITAL BUTTONS & SWITCHES", int(startX), 30)

	if g.configLoaded && len(g.settings.Mappings) > 0 {
		cols := 5
		for idx, m := range g.settings.Mappings {
			col := idx % cols
			row := idx / cols

			bx := startX + float32(col*85)
			by := float32(46 + row*26)

			pressed := false
			if m.Type == "button" {
				pressed = ebiten.IsGamepadButtonPressed(id, ebiten.GamepadButton(m.ID))
			} else if m.Type == "axis" {
				pressed = math.Abs(float64(getAxis(m.ID))) > 0.5
			}

			btnColor := color.RGBA{40, 44, 52, 255}
			if pressed {
				btnColor = color.RGBA{0, 180, 216, 255}
			}

			vector.DrawFilledRect(screen, bx, by, 78, 18, btnColor, false)

			label := fmt.Sprintf("%s:OFF", m.Name)
			if pressed {
				label = fmt.Sprintf("%s:ON", m.Name)
			}
			ebitenutil.DebugPrintAt(screen, label, int(bx)+4, int(by)+2)
		}
	} else {
		cols := 5
		for b := 0; b < buttonCount; b++ {
			col := b % cols
			row := b / cols

			bx := startX + float32(col*85)
			by := float32(46 + row*26)

			pressed := ebiten.IsGamepadButtonPressed(id, ebiten.GamepadButton(b))
			btnColor := color.RGBA{40, 44, 52, 255}
			if pressed {
				btnColor = color.RGBA{0, 180, 216, 255}
			}

			vector.DrawFilledRect(screen, bx, by, 78, 18, btnColor, false)
			label := fmt.Sprintf("Btn %d", b)
			if pressed {
				label = fmt.Sprintf("Btn %d ON", b)
			}
			ebitenutil.DebugPrintAt(screen, label, int(bx)+6, int(by)+2)
		}
	}

	// -------------------------------------------------------------------------
	// Bottom Left: Settings Badge & Gimbal Plots
	// -------------------------------------------------------------------------
	infoY := float32(235)
	configStatusText := "Config: not loaded"
	if g.configLoaded {
		configStatusText = "Config: loaded"
	}

	vector.DrawFilledRect(screen, 20, infoY, 320, 26, color.RGBA{30, 38, 50, 255}, false)
	vector.StrokeRect(screen, 20, infoY, 320, 26, 1, color.RGBA{0, 180, 216, 255}, false)
	ebitenutil.DebugPrintAt(screen, fmt.Sprintf("SETTINGS: [Mode: %s] | %s", g.settings.Mode, configStatusText), 28, int(infoY)+5)

	plotY := float32(290)
	boxSize := float32(115)

	// Determine slider bar rendering and gimbal positions based on active sliders
	hasSlider1 := g.configLoaded && g.settings.SliderAxis >= 0
	hasSlider2 := g.configLoaded && g.settings.Slider2Axis >= 0

	leftGimbalX := float32(55)
	rightGimbalX := float32(190)

	if hasSlider1 && hasSlider2 {
		leftGimbalX = float32(65)
		rightGimbalX = float32(195)

		// Slider 1 BAR
		latX1 := float32(15)
		latWidth := float32(16)
		ebitenutil.DebugPrintAt(screen, "S1", int(latX1), int(plotY-16))
		vector.DrawFilledRect(screen, latX1, plotY, latWidth, boxSize, color.RGBA{32, 36, 44, 255}, false)
		vector.StrokeRect(screen, latX1, plotY, latWidth, boxSize, 1, color.RGBA{70, 78, 90, 255}, false)

		rawVal1 := getAxis(g.settings.SliderAxis)
		minV1, maxV1 := g.settings.SliderMin, g.settings.SliderMax
		if maxV1 == minV1 { maxV1 = minV1 + 0.001 }
		norm1 := (rawVal1 - minV1) / (maxV1 - minV1)
		if norm1 < 0 { norm1 = 0 }
		if norm1 > 1 { norm1 = 1 }
		fillH1 := float32(norm1) * boxSize
		vector.DrawFilledRect(screen, latX1, plotY+boxSize-fillH1, latWidth, fillH1, color.RGBA{0, 200, 120, 255}, false)

		// Slider 2 BAR
		latX2 := float32(38)
		ebitenutil.DebugPrintAt(screen, "S2", int(latX2), int(plotY-16))
		vector.DrawFilledRect(screen, latX2, plotY, latWidth, boxSize, color.RGBA{32, 36, 44, 255}, false)
		vector.StrokeRect(screen, latX2, plotY, latWidth, boxSize, 1, color.RGBA{70, 78, 90, 255}, false)

		rawVal2 := getAxis(g.settings.Slider2Axis)
		minV2, maxV2 := g.settings.Slider2Min, g.settings.Slider2Max
		if maxV2 == minV2 { maxV2 = minV2 + 0.001 }
		norm2 := (rawVal2 - minV2) / (maxV2 - minV2)
		if norm2 < 0 { norm2 = 0 }
		if norm2 > 1 { norm2 = 1 }
		fillH2 := float32(norm2) * boxSize
		vector.DrawFilledRect(screen, latX2, plotY+boxSize-fillH2, latWidth, fillH2, color.RGBA{0, 220, 150, 255}, false)

	} else if hasSlider1 {
		leftGimbalX = float32(55)
		rightGimbalX = float32(190)

		latX := float32(20)
		latWidth := float32(16)
		ebitenutil.DebugPrintAt(screen, "S1 AUX", int(latX), int(plotY-16))
		vector.DrawFilledRect(screen, latX, plotY, latWidth, boxSize, color.RGBA{32, 36, 44, 255}, false)
		vector.StrokeRect(screen, latX, plotY, latWidth, boxSize, 1, color.RGBA{70, 78, 90, 255}, false)

		rawVal := getAxis(g.settings.SliderAxis)
		minV, maxV := g.settings.SliderMin, g.settings.SliderMax
		if maxV == minV { maxV = minV + 0.001 }
		norm := (rawVal - minV) / (maxV - minV)
		if norm < 0 { norm = 0 }
		if norm > 1 { norm = 1 }
		fillH := float32(norm) * boxSize
		vector.DrawFilledRect(screen, latX, plotY+boxSize-fillH, latWidth, fillH, color.RGBA{0, 200, 120, 255}, false)
	}

	// -------------------------------------------------------------------------
	// GIMBAL PLOTS (Physical Sticks Anchored, Channel Mapping per Mode)
	// -------------------------------------------------------------------------
	leftX := getAxis(0)  // Left X = Axis 0
	rightX := getAxis(3) // Right X = Axis 3

	var leftY, rightY float32
	var leftTitle, rightTitle string

	if strings.EqualFold(g.settings.Mode, "Mode1") {
		leftY = getAxis(1)
		rightY = getAxis(2)
		leftTitle = "LEFT (Yaw/Pitch)"
		rightTitle = "RIGHT (Roll/Thr)"
	} else {
		leftY = getAxis(2)
		rightY = getAxis(1)
		leftTitle = "LEFT (Yaw/Thr)"
		rightTitle = "RIGHT (Roll/Pitch)"
	}

	drawGimbal := func(label string, xPos float32, axisX, axisY float32) {
		ebitenutil.DebugPrintAt(screen, label, int(xPos), int(plotY-16))

		vector.DrawFilledRect(screen, xPos, plotY, boxSize, boxSize, color.RGBA{28, 32, 40, 255}, false)
		vector.StrokeRect(screen, xPos, plotY, boxSize, boxSize, 1, color.RGBA{80, 88, 100, 255}, false)

		centerX := xPos + boxSize/2
		centerY := plotY + boxSize/2
		vector.StrokeLine(screen, xPos, centerY, xPos+boxSize, centerY, 1, color.RGBA{90, 100, 115, 255}, false)
		vector.StrokeLine(screen, centerX, plotY, centerX, plotY+boxSize, 1, color.RGBA{90, 100, 115, 255}, false)

		dotX := centerX + (axisX * (boxSize/2 - 4))
		dotY := centerY - (axisY * (boxSize/2 - 4))

		vector.DrawFilledCircle(screen, dotX, dotY, 4, color.RGBA{230, 230, 230, 255}, false)
		vector.StrokeCircle(screen, dotX, dotY, 5, 1.5, color.RGBA{0, 220, 130, 255}, false)
	}

	drawGimbal(leftTitle, leftGimbalX, leftX, leftY)
	drawGimbal(rightTitle, rightGimbalX, rightX, rightY)

	// -------------------------------------------------------------------------
	// Bottom Right: Console Output & Command Input
	// -------------------------------------------------------------------------
	consoleX := float32(360)

	ebitenutil.DebugPrintAt(screen, "CONSOLE OUTPUT", int(consoleX), int(infoY)-2)
	outY := infoY + 16
	outHeight := float32(130)
	outWidth := float32(450)

	vector.DrawFilledRect(screen, consoleX, outY, outWidth, outHeight, color.RGBA{12, 14, 18, 255}, false)
	vector.StrokeRect(screen, consoleX, outY, outWidth, outHeight, 1, color.RGBA{50, 56, 68, 255}, false)

	for i, line := range g.outputLines {
		ebitenutil.DebugPrintAt(screen, line, int(consoleX)+8, int(outY)+6+(i*12))
	}

	inpY := outY + outHeight + 8
	ebitenutil.DebugPrintAt(screen, "COMMAND INPUT", int(consoleX), int(inpY)-2)
	fieldY := inpY + 14
	fieldHeight := float32(24)

	vector.DrawFilledRect(screen, consoleX, fieldY, outWidth, fieldHeight, color.RGBA{24, 28, 36, 255}, false)
	vector.StrokeRect(screen, consoleX, fieldY, outWidth, fieldHeight, 1, color.RGBA{0, 180, 216, 255}, false)

	prompt := fmt.Sprintf("> %s_", g.inputText)
	ebitenutil.DebugPrintAt(screen, prompt, int(consoleX)+8, int(fieldY)+5)
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return 830, 460
}

func main() {
	ebiten.SetWindowSize(830, 460)
	ebiten.SetWindowTitle("RadioMaster Pocket & Controller Diagnostic Console")
	if err := ebiten.RunGame(NewGame()); err != nil {
		log.Fatal(err)
	}
}