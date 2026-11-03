# rmt extensions

Extensions are trusted Lua files in `~/.rmt/extensions/`. rmt loads `*.lua` files in filename order on startup. Install an extension on each machine whose rmt process needs it: preparation and backend callbacks run on the host; client callbacks run on the machine with your terminal. Files are not copied to other devices. Restart the rmt host service after changing host extensions and start a new rmt client after changing client extensions.

An extension can register a program or add a preparation callback. Built-in OpenCode and Codex support remains available without extensions. A custom program needs both its host and client sides installed; they may be in the same file on each machine. User extensions are executable configuration: load only code you trust. rmt does not treat Lua as a security sandbox.

The Lua runtime provides Lua 5.1, its base, string, table and math libraries. Each call uses a fresh Lua state; globals do not persist between calls. A callback is limited to 30 seconds of Lua execution, but a host command started with `ctx.run` can take longer until it exits or is interrupted.

`rmt.getenv(name)` returns an environment variable from the machine running the callback, or `nil` if it is unset. Handle credentials carefully; extension code runs with the user's permissions.

## Preparing launches

```lua
rmt.on("prepare", function(ctx)
  if ctx.program ~= "codex" then return nil end
  return { directory = ctx.directory, args = ctx.args }
end)
```

The callback receives `ctx.program`, the absolute host `ctx.directory`, and `ctx.args` after the host shell expands the original command. Return `nil` to leave a launch alone, or a table containing `directory`, `args`, or both. Callbacks run in filename and registration order; later callbacks see earlier changes. The final directory must be absolute. Errors stop the launch and are shown to the user. Arguments such as `-w` are not interpreted by rmt; an extension can opt into handling them.

On the host, `ctx.run({"program", "argument", ...})` runs a command in the prompt's shell environment in `ctx.directory` and returns `stdout, stderr, status`. The argv elements are quoted as individual shell words. This can invoke shell functions as well as executables. Output is bounded; a command can be interrupted with Ctrl-C. The host's persistent prompt directory is not changed by a callback. Commands receive no interactive stdin.

## Registering a program

```lua
rmt.program({
  name = "example",
  host = function(ctx)
    return { url = "http://127.0.0.1:48555" }
  end,
  client = function(ctx)
    local argv = { "example", "--endpoint-env", "RMT_ENDPOINT" }
    for _, value in ipairs(ctx.args) do table.insert(argv, value) end
    return argv
  end,
})
```

Names start with a lowercase letter and contain lowercase letters, digits, `-` or `_`; built-in program names and common shell keywords and builtins are reserved. `host` and `client` are optional individually, so different files may implement each side. A host callback receives `ctx.program`, `ctx.directory` and `ctx.run(argv)` and returns either `{url = "http://127.0.0.1:PORT"}` or `{socket = "/absolute/private/unix/socket"}`. In this callback, `ctx.run` executes an executable with the host service's environment, not prompt shell functions, and follows the startup request's deadline. URLs must resolve to this host's loopback or local IP, and Unix sockets must be owned by the user and owner-only. An optional `authorization` string supplies the host backend's Authorization header. Start or discover your backend in this callback; rmt does not take ownership of a service your extension starts.

A client callback receives `ctx.program`, `ctx.directory`, `ctx.args`, and `ctx.endpoint`, and returns an argv array for the local native client. The host supplies the directory and arguments, so validate them before passing them to programs that interpret arguments as commands or local paths. rmt passes the authenticated loopback bridge address in `RMT_ENDPOINT` and the host directory in `RMT_DIRECTORY`. Prefer using those environment variables rather than putting the address in argv: the address contains a per-attachment secret. The client has the current terminal attached to stdin, stdout and stderr. rmt closes its bridge after the client exits; it does not stop the host service.

The bridge forwards HTTP and WebSocket traffic over the existing paired connection. A program needs a compatible native client/backend protocol; registering a program does not convert an arbitrary remote terminal app into a local TUI. Custom programs do not have built-in capability or version detection, so their extensions should report incompatible dependencies clearly. For a minimal example see [`docs/examples/extensions/program.lua`](examples/extensions/program.lua).

If `programs` is omitted in settings, registered custom programs and both built-ins are available. If it is set, list the desired custom names on the host as well. Registering a name makes rmt intercept a direct launch or direct shell alias with that name. Pipes, redirection, and compound commands remain host-shell commands or are rejected for native launches.
