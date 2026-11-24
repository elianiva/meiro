# AI control

## Preconditions

Use fake YouTube for tool tests. AI control defaults to off. For native
verification, start the desktop binary with a test profile.

## Actions

Open Settings, search for MCP, and turn on AI control. Copy the setup prompt.
Expand Advanced and copy the JSON config. Start the executable with --mcp as a
stdio server. List and call the tools, then turn off AI control while connected.

## Assertions

The prompt contains the exact executable and argument array.
Advanced exposes the same launch configuration. Initialize before listing or calling the 15 tools. Check collection search and
playback, radio, and the signed-out rating error. Check the recommendation
boundary, stopped seek error, and queue clear confirmation. Turn AI control off
during a call and verify that the connection closes. Turn it back on and call a
tool with the same bridge. Verify each queue page after restoring playback.

## Native checks

Spawn the GUI binary with redirected pipes. Check JSON-only stdout, EOF exit,
and the same-user pipe or socket. Run each package's executable and argument array.
