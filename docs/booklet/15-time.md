# 15. Time and identifiers

[The booklet](README.md) · previous: [14. Text and data](14-text.md) · next: [16. Databases](16-databases.md)

Dates and times are harder than they look: months have different
lengths, clocks jump an hour twice a year, and noon in Amsterdam is not
noon in Tokyo. The module `time` takes care of that. The module `uuid`
makes identifiers that are unique without asking anyone, for records,
files and messages.

## Now, and waiting

```
use time;

main() {
    local start = time::now();
    println("it is", start->format("%H:%M on %A %e %B %Y"));
    time::sleep(0.2);                              // seconds
    println("that took", time::now()->sub(start), "seconds");
    println(time(), clock());                      // built-ins: seconds since 1970, since the start
}
```

It prints something like

```text
it is 20:11 on Friday  6 November 2026
that took 0.200707139 seconds
1.7939923184486878e+09 0.200816823
```

`time::now()` is the current time in the computer's time zone.
`time::sleep(seconds)` waits. The built-ins `time()` and `clock()` give
plain numbers of seconds, for measuring how long something takes.

## A point in time

A `time::Time` is an instant together with a time zone:

```
use time;

main() {
    local t = new time::Time(
        2026,
        10,
        14,
        9,
        30,
        0,
        "Europe/Amsterdam",
    );
    println(t);
    println(
        t->year(),
        t->month(),
        t->day(),
        t->hour(),
        t->weekday(),
        t->yearday(),
    );
    println(t->format("%d.%m.%Y %H:%M %Z"), "/", t->format("%A %e %B, %I:%M %p"));
    println(t->inzone("Asia/Tokyo"), t->utc());
    println(t->add(3600)->format("%H:%M"), t->add(-86400 * 14)->format("%d %b"));
}
```

```
$ kukel run instant.kkl
time::Time(2026-10-14T09:30:00+02:00)
2026 10 14 9 3 287
14.10.2026 09:30 CEST / Wednesday 14 October, 09:30 AM
time::Time(2026-10-14T16:30:00+09:00) time::Time(2026-10-14T07:30:00Z)
10:30 30 Sep
```

- `new time::Time(year, month, day, hour, minute, second, zone)` makes
  one; the zone is a name such as `"Europe/Amsterdam"`, `"America/New_York"`
  or `"UTC"` (the default). kukel carries the time zone database, so the
  names work on every system.
- The member functions give the parts: `weekday()` is 0 for Sunday.
- `format` writes a time with `%` codes: `%Y` the year, `%m` the month,
  `%d` the day, `%H:%M:%S` the time, `%A` the weekday, `%B` the month's
  name, `%Z` the zone; the [reference](../reference.md#time) has them all.
- `inzone`, `utc` and `inlocal` give the same instant on another clock.
- `add(seconds)` and `sub(other)` compute with times; durations are
  seconds.

## Reading dates

`time::Time::parse` reads a time from text, by the same `%` codes:

```
use time;

main() {
    local due = time::Time::parse("30.11.2026", "%d.%m.%Y");
    local sent = time::Time::parse("2026-11-06T15:30:00+01:00");
    println(due, sent);
    println(floor(due->sub(sent) / 86400), "days to go", due->after(sent));

    try {
        time::Time::parse("31.11.2026", "%d.%m.%Y");
    } catch (e) {
        println(e->kind, e->message);
    }
}
```

```
$ kukel run parse.kkl
time::Time(2026-11-30T00:00:00Z) time::Time(2026-11-06T15:30:00+01:00)
23 days to go true
ValueError time::Time::parse: "31.11.2026": day 31 out of range for that month
```

Without a format, `parse` reads the standard form (RFC 3339) that
`println` shows and that JSON and databases often use. A date that does
not exist, such as the 31st of November, is a `ValueError`.

## Identifiers: `uuid`

A UUID is a 128-bit number written as 36 characters. Made the right way,
no two are ever the same, so a program can name a record before it is
stored, and two computers never pick the same name:

```
use uuid;

main() {
    local id = uuid::v7();                         // new each time
    println(len(id), uuid::valid(id), uuid::valid("not a uuid"));
    println(uuid::v5(uuid::DNS, "example.com"));   // always the same
    println(uuid::v5(uuid::URL, "https://example.com/ann") == uuid::v5(uuid::URL, "https://example.com/ann"));
}
```

```
$ kukel run ids.kkl
36 true false
cfbff0d1-9375-5685-968c-48ce8b15ae17
true
```

- `uuid::v7()` is the one to use for new records: it starts with the
  time, so ids made later sort later, which databases like.
- `uuid::v4()` is entirely random.
- `uuid::v5(namespace, name)` is always the same for the same name: an id
  derived from something that already has a name, such as a URL.

[The booklet](README.md) · previous: [14. Text and data](14-text.md) · next: [16. Databases](16-databases.md)
