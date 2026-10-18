//go:build tk

package tkmod

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Image is the class tk::Image: a picture from a PNG or GIF file, for
// labels, buttons and canvases.
var Image = &native.Class{Name: "Image"}

// maxImageFile is the largest image file read (bytes).
const maxImageFile = 64 << 20

// picture is the state of a tk::Image.
type picture struct {
	img           *tk.Img
	path          string
	width, height int64
	freed         bool // free() deleted it in Tk
}

// readImageFile reads a regular file (not a device or pipe, which could
// be endless or block), at most one byte more than maxImageFile.
func readImageFile(path string) ([]byte, error) {
	// Before opening: opening a pipe to read waits for a writer.
	if info, err := os.Stat(path); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxImageFile+1))
}

// imageKind tells PNG and GIF data by their signatures ("" for neither).
func imageKind(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "PNG"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "GIF"
	}
	return ""
}

// new tk::Image(path): the picture in a PNG or GIF file. The file is read
// by kukel (an IOError when it cannot be), and its bytes go to Tk.
func newImage(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
	a, err := native.Check("tk::Image::Image", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	path, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	data, err := readImageFile(path)
	if err != nil {
		return value.NilValue, a.Errorf(value.IOError, "%v", err)
	}
	if len(data) > maxImageFile {
		return value.NilValue, a.Errorf(value.ValueError, "%s is larger than %d MB", path, maxImageFile>>20)
	}
	if imageKind(data) == "" {
		return value.NilValue, a.Errorf(value.ValueError, "%s is not a PNG or GIF file", path)
	}
	if err := start(a, env); err != nil {
		return value.NilValue, err
	}
	img := tk.NewPhoto(tk.Data(data))
	if err := tkError(); err != nil || img == nil {
		why := "Tk cannot read it"
		if err != nil {
			why += ": " + err.Error()
		}
		return value.NilValue, a.Errorf(value.ValueError, "%s: %s", path, why)
	}
	p := &picture{img: img, path: path}
	p.width, _ = strconv.ParseInt(img.Width(), 10, 64)
	p.height, _ = strconv.ParseInt(img.Height(), 10, 64)
	this.State = p
	return value.NilValue, nil
}

// imageValue is an option whose value is a tk::Image.
func imageValue(v value.Value) (any, string) {
	if !Image.Is(v) {
		return nil, " must be a tk::Image, not " + v.Repr()
	}
	if p := v.AsObject().State.(*picture); !p.freed {
		return p, ""
	}
	return nil, ": the image is freed"
}

// The options image and compound (where the image is next to the text) of
// labels and buttons.
var (
	optImage    = option{"image", imageValue, func(x any) tk.Opt { return tk.Image(x.(*picture).img) }}
	optCompound = option{"compound", oneOf("none", "left", "right", "top", "bottom", "center", "image", "text"), func(x any) tk.Opt { return tk.Compound(x) }}
)

func registerImages() {
	Image.New = newImage
	Image.Methods = map[string]value.NativeMethod{
		"width": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("tk::Image::width", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			return value.MakeInt(this.State.(*picture).width), nil
		},
		"height": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("tk::Image::height", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			return value.MakeInt(this.State.(*picture).height), nil
		},
		// free(): deletes the picture in Tk; what shows it goes blank, and
		// it can no longer be given to a widget or a canvas.
		"free": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			a, err := native.Check("tk::Image::free", args, 0, 0)
			if err != nil {
				return value.NilValue, err
			}
			p := this.State.(*picture)
			if p.freed {
				return value.NilValue, nil
			}
			if !gui.ended { // else Tk, and the picture, are gone already
				if err := start(a, env); err != nil {
					return value.NilValue, err
				}
				p.img.Delete()
				tkError()
			}
			p.freed = true
			return value.NilValue, nil
		},
	}
	Image.Show = func(o *value.ObjectObj) string {
		p := o.State.(*picture)
		if p.freed {
			return fmt.Sprintf("tk::Image(%s, freed)", value.Quote(p.path))
		}
		return fmt.Sprintf("tk::Image(%s, %dx%d)", value.Quote(p.path), p.width, p.height)
	}
}
