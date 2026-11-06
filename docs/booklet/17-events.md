# 17. Events and workers

[The booklet](README.md) · previous: [16. Databases](16-databases.md) · next: [18. Networking](18-networking.md)

A program often waits for several things at once: a timer, a message from
the network, a click in a window. Kukel has two answers. The *event loop*
(module `events`) lets one program wait for all of them and handle each
as it comes, one at a time. *Workers* (module `worker`) run functions in
parallel, each in an interpreter of its own, to use all the processor's
cores.

## The event loop

```
use events;

main() {
    local n = 0, tick;
    tick = events::every(0.1, fn() {
        n++;
        println("tick", n);

        if (n == 3) tick->cancel();
    });
    events::after(0.25, fn() { println("a quarter second"); });
    events::post(println, "as soon as the loop runs");
    println("start");
    events::run();
    println("nothing left to wait for");
}
```

```
$ kukel run timers.kkl
start
as soon as the loop runs
tick 1
tick 2
a quarter second
tick 3
nothing left to wait for
```

- `events::after(seconds, f, args...)` calls `f(args...)` once, after
  that long; `events::every(seconds, f, ...)` again and again. Both give an
  `events::Timer`, which `cancel()` stops.
- `events::post(f, args...)` calls `f` soon, after what is already
  waiting.
- `events::run()` is the loop: it calls the functions as their time comes,
  and returns when nothing is left to wait for (or after
  `events::stop()`).

The functions run one at a time, never at once, so they can share
variables without any care. The other side of that: a function that
takes long holds everything up. Keep handlers short, and give long work
to a worker.

Other modules deliver their events through the same loop: connections
and packets of `net`, sockets of `zmq` (next chapter), messages of
workers, and windows of `tk` (chapter 21). One program can mix them all.

## When a handler fails

An exception that a handler does not catch ends `events::run()`, which
throws it on. `events::onerror(f)` gets such exceptions instead, and the
loop goes on:

```
use events;

main() {
    events::onerror(fn(e) { println("handled:", e->message); });
    events::after(0.01, fn() { throw new Error("first one fails"); });
    events::after(0.02, fn() { println("the loop goes on"); });
    events::run();
}
```

```
$ kukel run onerror.kkl
handled: first one fails
the loop goes on
```

## Workers

`worker::start(f, args...)` runs `f(args...)` in a new interpreter, on a
thread of its own, at the same time as the rest of the program.
`w->wait()` waits for it to finish and gives what `f` returned:

```
use worker;

primes(from, to) {
    local count = 0;

    for (local n = max(from, 2); n < to; n++) {
        local prime = true;

        for (local d = 2; d * d <= n; d++) {
            if (n % d == 0) {
                prime = false;
                break;
            }
        }

        if (prime) count++;
    }
    return count;
}

main() {
    local workers = [];

    for (local i = 0; i < 4; i++) push(workers, worker::start(primes, i * 50000, (i + 1) * 50000));
    local total = 0;

    for (local w in workers) total += w->wait();
    println("primes below 200000:", total);
}
```

```
$ kukel run primes.kkl
primes below 200000: 17984
```

On a computer with four cores this runs about four times as fast as one
call of `primes(0, 200000)`.

Workers share nothing. Each has its own globals; what goes in (the
arguments) and comes out (the result) is copied. Only plain data can
travel: `nil`, booleans, numbers, strings, and vectors and maps of those.
Objects and functions cannot. Because nothing is shared, nothing needs a
lock.

## Messages

A worker and the program that started it can also talk while both run,
with messages:

```
use worker;

squarer() {                                       // runs in the worker
    for (local m = worker::receive(); m != nil; m = worker::receive()) worker::send(m * m);
    return "done";
}

main() {
    local w = worker::start(squarer);

    for (local i = 1; i <= 3; i++) w->send(i);

    for (local i = 1; i <= 3; i++) println(w->receive());
    w->stop();                                    // the worker's receive() now gives nil
    println(w->wait());
}
```

```
$ kukel run squarer.kkl
1
4
9
done
```

- `w->send(v)` sends a copy of `v` to the worker; in the worker,
  `worker::receive()` takes the next one, and `worker::send(v)` answers.
- `w->receive()` takes the next answer. Both `receive`s wait, or take a
  timeout in seconds and then give `nil`.
- `w->stop()` says there will be no more messages: the worker's
  `receive()` gives `nil`, and it can finish.
- `w->onmessage(f)` and `worker::onmessage(f)` hand the messages to the
  event loop instead.

A worker can also run another program: `worker::startfile("job.kkl",
args...)`.

[The booklet](README.md) · previous: [16. Databases](16-databases.md) · next: [18. Networking](18-networking.md)
