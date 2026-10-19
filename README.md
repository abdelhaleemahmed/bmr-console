# bmr-console

**One screen that answers the only question a machine in the middle of a
bare-metal recovery has to answer: can this thing receive a restore, and if
not, which part is wrong?**

A recovery environment hands you a Bareos file daemon with no user interface
and a director somewhere else. Finding out whether the daemon came up means
reading a configuration file in a directory you have to remember, working out
whether anything is listening, and then getting the director to agree that
this client is the client it thinks it is. Three tools and a lot of typing,
at the point in the day when typing is least reliable.

This is one static binary, no dependencies, that answers it on both halves of
a recovery: a Windows PE image and a Linux rescue system built with
[ReaR][rear].

[rear]: https://relax-and-recover.org/

---

## The short version

```
$ bmr-console check
------------------------------------------------------------------------
CAN A DIRECTOR USE THIS MACHINE
------------------------------------------------------------------------
  [ OK ] daemon on 9102         accepted a connection on 127.0.0.1:9102
  [ OK ] on 10.0.2.15           a director can reach this
  [ OK ] on 192.168.56.26       a director can reach this

this machine can receive a restore
```

Run with no arguments it offers a numbered menu. The subcommands are what you
want from a script or over ssh:

| command | what it answers |
|---|---|
| `bmr-console env` | where the daemon, its configuration and `bconsole` are **on this machine**, and which of them could not be found. It prints the paths it chose rather than assuming them, because the whole class of failure in a recovery is a tool reading the right file on the wrong disk |
| `bmr-console config` | what this client believes it is: name, port, working directory, and every director allowed to connect |
| `bmr-console status` | whether a director can use this machine. Exits non-zero when it cannot |
| `bmr-console jobs` | what the director has for this client. Needs a `bconsole`, which a recovery client does not normally have |
| `bmr-console check` | all of the above, ending in one line, with an exit status that says the same thing so it can gate a script |

---

## Three decisions worth explaining

### It asks the daemon, not the filesystem

Configuration comes from `bareos-fd --xc`, which prints the resources **as
the daemon resolved them**. A `.conf` file on disk tells you what somebody
intended; the daemon tells you what is actually in effect, including the
include directory it really read. In a recovery those differ more often than
they agree.

Passwords print as `(set, md5, 37 characters)`. Whether one is set is a real
question in a recovery. What it is never is.

### It dials the port, it does not read netstat

`status` opens a TCP connection to the daemon's port on **every address the
machine has**, because that is what the director will do, and it fails the
same way. A listening socket in `netstat` and a connection a director can
actually complete are not the same claim.

It also names a link-local address explicitly. `169.254.x.x` means the static
address was refused -- almost always another machine holding it -- and
Windows fell back to something nothing can route to. That failure otherwise
presents as a working network with an unreachable client.

### No GUI, on purpose

Every cross-platform Go GUI toolkit draws through OpenGL. A WinPE has a basic
display driver and no 3D at all, and a Linux rescue image is often a
framebuffer console with no X server -- so a graphical build would fail to
start on exactly the machines this is for. A numbered menu on stdin/stdout
works on both, over a serial console, and over ssh, which is how a recovery
is usually driven anyway.

---

## Building

```sh
./build.sh
```

Produces `dist/bmr-console-windows-amd64.exe` and
`dist/bmr-console-linux-amd64`. `CGO_ENABLED=0` is not decoration: a cgo
build links against the build host's libc, and a rescue image is precisely
the environment where that libc is not present.

`go.mod` declares no requirements, deliberately. This binary is carried into
an environment that may have no network at all, and a module it has to fetch
at build time is a dependency the recovery inherits.

---

## Getting it into a recovery image

**ReaR** -- install the Linux build, then one line in `/etc/rear/local.conf`:

```bash
COPY_AS_IS=( "${COPY_AS_IS[@]}" /usr/local/bin/bmr-console )
```

`COPY_AS_IS` takes a path as given. Its neighbour `REQUIRED_PROGS` asks ReaR
to *find* a program and fails the build when it cannot, which is right for
`bareos-fd` and wrong for a binary that is not on a stock machine's PATH.

**Windows PE** -- copy the `.exe` into `System32` of the image. It needs
nothing else; it is static.

---

## What it does not do

It does not start a restore and it does not drive a director. The Bareos
**client** package for Windows ships no `bconsole` at all, so `jobs` is the
one subcommand that can report a missing tool rather than a missing answer.
Driving the director from the machine being recovered is not something this
does, and in a real recovery it is not something you want.

---

## Licence

MIT. See [LICENSE](LICENSE).
