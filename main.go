package main

import (
	"embed"
	"flag"
	"fmt"
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/dlasky/gotk3-layershell/layershell"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

//go:embed embed/*
var embedFS embed.FS

var (
	buttons          []Button
	src              glib.SourceHandle
	outerOrientation gtk.Orientation
	innerOrientation gtk.Orientation
)

type Button struct {
	Icon  string
	Label string
	Exec  string
}

// Flags
var (
	debug        = flag.Bool("debug", false, "display debug information")
	position     = flag.String("p", "left", "position: 'bottom', 'top', 'left', 'right', 'center'")
	full         = flag.Bool("f", false, "if position is not 'center' - fill to the edges")
	alignment    = flag.String("a", "middle", "if filled to the edges - align to 'start' or 'end'")
	marginTop    = flag.Int("mt", 0, "margin top")
	marginLeft   = flag.Int("ml", 0, "margin left")
	marginRight  = flag.Int("mr", 0, "margin right")
	marginBottom = flag.Int("mb", 0, "margin bottom")

	iconSize      = flag.Int("i", 48, "icon size")
	targetOutput  = flag.String("o", "", "name of the output to display the bar on (sway only)")
	order         = flag.String("order", "logout reboot shutdown sleep hybrid-sleep hibernate lock", "order of displayed actions")
	exclusiveZone = flag.Bool("x", false, "open on top layer with exclusive zone")

	theme     = flag.String("t", "dark", "default icon theme: 'light', 'dark', or 'custom'")
	styleFile = flag.String("style", "embed:style", "css style file name")

	lgi = flag.String("lgi", "", "logout icon name")
	rbi = flag.String("rbi", "", "reboot icon name")
	sdi = flag.String("sdi", "", "shutdown icon name")
	sli = flag.String("sli", "", "sleep icon name")
	hsi = flag.String("hsi", "", "hybrid sleep icon name")
	hbi = flag.String("hbi", "", "hibernate icon name")
	lci = flag.String("lci", "", "lock icon name")

	lgl = flag.String("lgl", "Logout", "logout label")
	rbl = flag.String("rbl", "Reboot", "reboot label")
	sdl = flag.String("sdl", "Shutdown", "shutdown label")
	sll = flag.String("sll", "Sleep", "sleep label")
	hsl = flag.String("hsl", "Hybrid Sleep", "hybrid sleep label")
	hbl = flag.String("hbl", "Hibernate", "hibernate label")
	lcl = flag.String("lcl", "Lock", "lock label")

	seat = flag.String("seat", "systemd", "default seat manager: 'systemd', 'elogind', or 'custom'")
	rbc  = flag.String("rbc", "systemctl reboot", "reboot command (requires 'custom' seat)")
	sdc  = flag.String("sdc", "systemctl -i poweroff", "shutdown command (requires 'custom' seat)")
	slc  = flag.String("slc", "systemctl suspend", "sleep command (requires 'custom' seat)")
	hsc  = flag.String("hsc", "systemctl hybrid-sleep", "hybrid sleep command (requires 'custom' seat)")
	hbc  = flag.String("hbc", "systemctl hibernate", "hibernate command (requires 'custom' seat)")
	lgc  = flag.String("lgc", "swaymsg exit", "logout command")
	lcc  = flag.String("lcc", "waylock --init-color #222222 --input-color #4c7899", "lock command")
)

func main() {
	flag.Parse()

	if !*debug {
		log.SetOutput(io.Discard)
	}

	if *theme == "dark" {
		*lgi = "embed:system-log-out-symbolic-dark"
		*rbi = "embed:system-reboot-symbolic-dark"
		*sdi = "embed:system-shutdown-symbolic-dark"
		*sli = "embed:system-suspend-symbolic-dark"
		*hsi = "embed:system-hibernate-symbolic-dark"
		*hbi = "embed:system-hibernate-symbolic-dark"
		*lci = "embed:system-lock-screen-symbolic-dark"
	} else if *theme == "light" {
		*lgi = "embed:system-log-out-symbolic-light"
		*rbi = "embed:system-reboot-symbolic-light"
		*sdi = "embed:system-shutdown-symbolic-light"
		*sli = "embed:system-suspend-symbolic-light"
		*hsi = "embed:system-hibernate-symbolic-light"
		*hbi = "embed:system-hibernate-symbolic-light"
		*lci = "embed:system-lock-screen-symbolic-light"
	}

	if *seat == "systemd" {
		*rbc = "systemctl reboot"
		*sdc = "systemctl -i poweroff"
		*slc = "systemctl suspend"
		*hsc = "systemctl hybrid-sleep"
		*hbc = "systemctl hibernate"
	} else if *seat == "elogind" {
		*rbc = "loginctl reboot"
		*sdc = "loginctl -i poweroff"
		*slc = "loginctl suspend"
		*hsc = "loginctl hybrid-sleep"
		*hbc = "loginctl hibernate"
	}

	// Gentle SIGTERM handler thanks to reiki4040 https://gist.github.com/reiki4040/be3705f307d3cd136e85
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM)
	go func() {
		for {
			s := <-signalChan
			if s == syscall.SIGTERM {
				println("SIGTERM received, bye bye!")
				gtk.MainQuit()
			}
		}
	}()

	// We want the same key/mouse binding to turn the dock off: kill the running instance and exit.
	lockFilePath := filepath.Join(tempDir(), "wlogoutbar.lock")
	lockFile, err := createLockFile(lockFilePath)
	if err != nil {
		pid, err := getLockFilePid(lockFilePath)
		if err == nil {
			log.Println("Running instance found, sending SIGTERM and exiting…")
			syscall.Kill(pid, syscall.SIGTERM)
		}
		os.Exit(0)
	}
	defer lockFile.Close()

	ord := strings.Fields(*order)
	for _, a := range ord {
		switch a {
		case "logout":
			buttons = append(buttons, Button{*lgi, *lgl, *lgc})
		case "reboot":
			buttons = append(buttons, Button{*rbi, *rbl, *rbc})
		case "shutdown":
			buttons = append(buttons, Button{*sdi, *sdl, *sdc})
		case "sleep":
			buttons = append(buttons, Button{*sli, *sll, *slc})
		case "hybrid-sleep":
			buttons = append(buttons, Button{*hsi, *hsl, *hsc})
		case "hibernate":
			buttons = append(buttons, Button{*hbi, *hbl, *hbc})
		case "lock":
			buttons = append(buttons, Button{*lci, *lcl, *lcc})
		}
	}

	gtk.Init(nil)

	// load style sheet
	log.Print(*styleFile)
	cssProvider, _ := gtk.CssProviderNew()
	_ = loadCssStyle(cssProvider, styleFile)
	screen, _ := gdk.ScreenGetDefault()
	gtk.AddProviderForScreen(screen, cssProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Fatal("Unable to create window:", err)
	}

	layershell.InitForWindow(win)

	// if -o argument given
	var output2mon map[string]*gdk.Monitor
	if *targetOutput != "" {
		// We want to assign layershell to a monitor, but we only know the output name!
		output2mon, err = mapOutputs()
		if err == nil {
			layershell.SetMonitor(win, output2mon[*targetOutput])
		} else {
			fmt.Println(err)
		}
	}

	if *position == "bottom" || *position == "top" {
		if *position == "bottom" {
			layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_BOTTOM, true)
		} else {
			layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_TOP, true)
		}

		outerOrientation = gtk.ORIENTATION_VERTICAL
		innerOrientation = gtk.ORIENTATION_HORIZONTAL

		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_LEFT, *full)
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_RIGHT, *full)
	}

	if *position == "left" || *position == "right" {
		if *position == "left" {
			layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_LEFT, true)
		} else {
			layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_RIGHT, true)
		}

		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_TOP, *full)
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_BOTTOM, *full)

		outerOrientation = gtk.ORIENTATION_HORIZONTAL
		innerOrientation = gtk.ORIENTATION_VERTICAL
	}

	if *position == "center" && *full {
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_RIGHT, true)
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_LEFT, true)
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_BOTTOM, true)
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_TOP, true)
	}

	layershell.SetMargin(win, layershell.LAYER_SHELL_EDGE_TOP, *marginTop)
	layershell.SetMargin(win, layershell.LAYER_SHELL_EDGE_LEFT, *marginLeft)
	layershell.SetMargin(win, layershell.LAYER_SHELL_EDGE_RIGHT, *marginRight)
	layershell.SetMargin(win, layershell.LAYER_SHELL_EDGE_BOTTOM, *marginBottom)

	if !*exclusiveZone {
		layershell.SetLayer(win, layershell.LAYER_SHELL_LAYER_OVERLAY)
		layershell.SetExclusiveZone(win, -1)
	} else {
		layershell.SetLayer(win, layershell.LAYER_SHELL_LAYER_TOP)
		layershell.SetExclusiveZone(win, 0)
	}

	layershell.SetKeyboardMode(win, layershell.LAYER_SHELL_KEYBOARD_MODE_EXCLUSIVE)

	win.Connect("destroy", func() {
		gtk.MainQuit()
	})

	// Close the window on leave, but not immediately, to avoid accidental closes
	win.Connect("leave-notify-event", func() {
		src = glib.TimeoutAdd(uint(500), func() bool {
			gtk.MainQuit()
			src = 0
			return false
		})
	})

	win.Connect("enter-notify-event", func() {
		cancelClose()
	})

	win.Connect("key-release-event", func(window *gtk.Window, event *gdk.Event) {
		key := &gdk.EventKey{Event: event}
		if key.KeyVal() == gdk.KEY_Escape {
			gtk.MainQuit()
		}
	})

	outerBox, _ := gtk.BoxNew(outerOrientation, 0)
	outerBox.SetProperty("name", "outer-box")
	win.Add(outerBox)

	alignmentBox, _ := gtk.BoxNew(innerOrientation, 0)
	outerBox.PackStart(alignmentBox, true, true, 0)

	mainBox, _ := gtk.BoxNew(innerOrientation, 0)
	mainBox.SetHomogeneous(true)
	mainBox.SetProperty("name", "inner-box")

	if *alignment == "start" {
		alignmentBox.PackStart(mainBox, false, true, 0)
	} else if *alignment == "end" {
		alignmentBox.PackEnd(mainBox, false, true, 0)
	} else {
		alignmentBox.PackStart(mainBox, true, false, 0)
	}

	mainBox.SetVAlign(gtk.ALIGN_CENTER)

	for _, b := range buttons {
		button, _ := gtk.ButtonNew()
		button.SetAlwaysShowImage(true)
		button.SetImagePosition(gtk.POS_TOP)

		var pixbuf *gdk.Pixbuf
		if b.Icon != "" {
			if strings.HasPrefix(b.Icon, "embed:") {
				pixbuf, err = createPixbufFromEmbed(strings.TrimPrefix(b.Icon, "embed:"), *iconSize)
			} else {
				pixbuf, err = createPixbuf(b.Icon, *iconSize)
			}
		} else {
			pixbuf, err = createPixbuf("image-missing", *iconSize)
		}
		if err != nil {
			log.Print(err)
			pixbuf, _ = createPixbuf("unknown", *iconSize)
		}
		img, _ := gtk.ImageNewFromPixbuf(pixbuf)
		button.SetImage(img)

		if b.Label != "" {
			button.SetLabel(b.Label)
		}

		button.Connect("enter-notify-event", func() {
			cancelClose()
		})

		exec := b.Exec

		button.Connect("clicked", func() {
			launch(exec)
		})

		mainBox.PackStart(button, true, true, 0)
	}

	win.ShowAll()
	gtk.Main()
}

func loadCssStyle(cssProvider *gtk.CssProvider, styleFile *string) error {
	var err error
	// Load the user style file if the styleFile is set to "embed:"
	// Check if the user has a style file in $XDG_CONFIG_HOME/wlogoutbar/style.css
	// and if it exists then try to load it
	if strings.HasPrefix(*styleFile, "embed:") {
		if err := tryLoadingUserCssFile(cssProvider); err != nil {
			style, err := embedFS.ReadFile("embed/" + strings.TrimPrefix(*styleFile, "embed:") + ".css")
			if err != nil {
				log.Printf("ERROR: %s css file not found. Using GTK styling.\n", *styleFile)
			} else {
				err = cssProvider.LoadFromData(string(style))
				if err != nil {
					log.Printf("ERROR: %s css file is erroneous. Using GTK styling.\n", *styleFile)
				}
			}
		}
	} else if *styleFile != "" {
		err := cssProvider.LoadFromPath(*styleFile)
		if err != nil {
			log.Printf("ERROR: %s css file not found or erroneous. Using GTK styling.\n", *styleFile)
			log.Printf("%s\n", err)
		}
	}
	return err
}

func tryLoadingUserCssFile(cssProvider *gtk.CssProvider) error {
	xdgConfigDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	userCssFile := filepath.Join(xdgConfigDir, "wlogoutbar", "style.css")
	log.Printf("Trying to load user CSS file at %s\n", userCssFile)

	if _, err := os.Stat(userCssFile); err == nil || errors.Is(err, fs.ErrExist) {
		if err = cssProvider.LoadFromPath(userCssFile); err != nil {
			log.Println("Failed to load user CSS file")
		} else {
			log.Println("Loaded user CSS file")
		}
	} else {
		log.Println("User CSS file does not exist, falling back to builtin theme")
		log.Printf("%s\n", err)
	}
	return err
}
