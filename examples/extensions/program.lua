-- Example only: replace the command and URL with a program that supports HTTP or WebSocket attach.
rmt.program({
  name = "example",
  host = function(ctx)
    -- Start or discover your backend here, e.g. with ctx.run({"example", "serve"}).
    return { url = "http://127.0.0.1:48555" }
  end,
  client = function(ctx)
    -- The client in this example reads the endpoint from RMT_ENDPOINT.
    local argv = { "example", "--endpoint-env", "RMT_ENDPOINT" }
    for _, value in ipairs(ctx.args) do table.insert(argv, value) end
    return argv
  end,
})
