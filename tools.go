package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/joshuarubin/go-sway"
)

// If filename is a lock file, returns the PID of the process locking it
func getLockFilePid(filename string) (pid int, err error) {
	contents, err := ioutil.ReadFile(filename)
	if err != nil {
		return
	}

	pid, err = strconv.Atoi(string(contents))
	return
}

// createLockFile tries to create a file with given name and acquire an
// exclusive lock on it. If the file already exists AND is still locked, it will
// fail.
func createLockFile(filename string) (*os.File, error) {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		file.Close()
		return nil, err
	}

	// Write PID to lock file
	contents := strconv.Itoa(os.Getpid())
	if err := file.Truncate(0); err != nil {
		file.Close()
		return nil, err
	}
	if _, err := file.WriteString(contents); err != nil {
		file.Close()
		return nil, err
	}

	return file, nil
}

func tempDir() string {
	if os.Getenv("TMPDIR") != "" {
		return os.Getenv("TMPDIR")
	} else if os.Getenv("TEMP") != "" {
		return os.Getenv("TEMP")
	} else if os.Getenv("TMP") != "" {
		return os.Getenv("TMP")
	} else if os.Getenv("XDG_RUNTIME_DIR") != "" {
		return os.Getenv("XDG_RUNTIME_DIR")
	}
	return "/tmp"
}

func mapOutputs() (map[string]*gdk.Monitor, error) {
	result := make(map[string]*gdk.Monitor)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	client, err := sway.New(ctx)
	if err != nil {
		return nil, err
	}

	outputs, err := client.GetOutputs(ctx)
	if err != nil {
		return nil, err
	}

	display, err := gdk.DisplayGetDefault()
	if err != nil {
		return nil, err
	}

	num := display.GetNMonitors()
	for i := 0; i < num; i++ {
		monitor, _ := display.GetMonitor(i)
		geometry := monitor.GetGeometry()
		// assign output to monitor on the basis of the same x, y coordinates
		for _, output := range outputs {
			if int(output.Rect.X) == geometry.GetX() && int(output.Rect.Y) == geometry.GetY() {
				result[output.Name] = monitor
			}
		}
	}
	return result, nil
}

/*
Window on-leave-notify event hides the dock with glib Timeout 500 ms.
We might have left the window by accident, so let's clear the timeout if window re-entered.
Furthermore - hovering a button triggers window on-leave-notify event, and the timeout
needs to be cleared as well.
*/
func cancelClose() {
	if src > 0 {
		glib.SourceRemove(src)
		src = 0
	}
}

func createPixbufFromEmbed(icon string, size int) (*gdk.Pixbuf, error) {
	img, err := embedFS.ReadFile("embed/" + icon + ".svg")
	if err != nil {
		return nil, err
	}
	pl, err := gdk.PixbufLoaderNewWithType("svg")
	if err != nil {
		return nil, err
	}
	pl.SetSize(size, size)
	_, err = pl.Write(img)
	pl.Close()
	if err != nil {
		return nil, err
	}
	return pl.GetPixbuf()
}

func createPixbuf(icon string, size int) (*gdk.Pixbuf, error) {
	if strings.HasPrefix(icon, "/") {
		pixbuf, err := gdk.PixbufNewFromFileAtSize(icon, size, size)
		if err != nil {
			fmt.Println(err)
			return nil, err
		}
		return pixbuf, nil
	}

	iconTheme, err := gtk.IconThemeGetDefault()
	if err != nil {
		log.Fatal("Couldn't get default theme: ", err)
	}
	pixbuf, err := iconTheme.LoadIcon(icon, size, gtk.ICON_LOOKUP_FORCE_SIZE)
	if err != nil {
		return nil, err
	}
	return pixbuf, nil
}

func launch(command string) {
	// trim % and everything afterwards
	if strings.Contains(command, "%") {
		cutAt := strings.Index(command, "%")
		if cutAt != -1 {
			command = command[:cutAt-1]
		}
	}

	elements := strings.Split(command, " ")

	// find prepended env variables, if any
	envVarsNum := strings.Count(command, "=")
	var envVars []string

	cmdIdx := 0
	lastEnvVarIdx := 0

	if envVarsNum > 0 {
		for idx, item := range elements {
			if strings.Contains(item, "=") {
				lastEnvVarIdx = idx
				envVars = append(envVars, item)
			}
		}
		cmdIdx = lastEnvVarIdx + 1
	}

	cmd := exec.Command(elements[cmdIdx], elements[1+cmdIdx:]...)

	// set env variables
	if len(envVars) > 0 {
		cmd.Env = os.Environ()
		cmd.Env = append(cmd.Env, envVars...)
	}

	msg := fmt.Sprintf("env vars: %s; command: '%s'; args: %s\n", envVars, elements[cmdIdx], elements[1+cmdIdx:])
	println(msg)

	cmd.Start()
	gtk.MainQuit()
}
