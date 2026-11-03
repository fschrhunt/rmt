-- Example: add a native application flag without affecting other programs.
rmt.on("prepare", function(ctx)
  if ctx.program ~= "codex" then return nil end
  local args = {}
  for _, value in ipairs(ctx.args) do table.insert(args, value) end
  -- Add or change arguments only when your setup needs it.
  return { args = args }
end)
