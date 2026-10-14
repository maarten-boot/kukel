// Package zmqmod is the native module "zmq": ZeroMQ sockets, to talk with
// other programs (in any language) and other machines. It uses the pure Go
// implementation github.com/go-zeromq/zmq4, so no C library is needed.
//
//	use zmq;
//	local s = new zmq::Socket("REQ");
//	s->connect("tcp://localhost:5555");
//	s->send("hello");
//	println(s->receive(5));          // the reply, or nil after 5 seconds
//	s->close();
//
// Messages are strings (bytes); a message of several parts is a vector of
// strings. Sockets of type REQ/REP, PUB/SUB, PUSH/PULL, DEALER/ROUTER and
// PAIR are supported; zmq::poll waits for the first of several sockets.
//
// The build tag nozmq leaves the module out (its code is in zmq.go).
package zmqmod
