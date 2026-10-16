// Package tkmod is the native module "tk": desktop windows with Tk widgets
// whose event handlers are Kukel functions (TODO.md Q67).
//
//	use tk; use events;
//	main() {
//	    local w = new tk::Window("Kukel"), n = 0;
//	    local label = new tk::Label(w, "clicked 0 times");
//	    local button = new tk::Button(w, "Click me");
//	    button->onclick(fn() { n++; label->settext("clicked " + str(n) + " times"); });
//	    label->pack();
//	    button->pack();
//	    events::run();          // until the window is closed
//	}
//
// It uses modernc.org/tk9.0, Tk without cgo: Tcl/Tk are embedded in kukel
// and unpacked to the user's cache directory the first time. Tk runs on the
// main thread of the process, so only an interpreter running in the main
// goroutine can use it (kukel's own, not a worker; a Go program that embeds
// kukel calls Run there). events::run() asks Tk for its events every 10
// milliseconds, so handlers run one at a time like all handlers of the
// event loop.
//
// The module adds about 26 MB, so it is only in a kukel built with the
// build tag tk (go build -tags tk); its code is in the files with the build
// constraint tk.
package tkmod
