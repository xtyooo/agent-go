# Milestone 4 Observability

This milestone follows the Java `dodo-agent` WebSearch ReAct chain:

```text
AgentController.webSearchStream
-> WebSearchReactAgent.streamInternal
-> scheduleRound
-> processChunk
-> finishRound
-> executeToolCalls
```

The Go replica keeps the same runtime boundaries and adds structured console logs with `slog`.

## Log Boundaries

HTTP/SSE:

- `chat stream request accepted`: request was validated and the agent was started.
- `sse stream opened`: response headers were prepared for `text/event-stream`.
- `sse stream closed`: event channel closed normally.
- `sse stream client cancelled`: browser/client disconnected or request context ended.
- `chat stream request finished`: request-level summary with total event count and elapsed time.

Agent loop:

- `websearch react agent started`: agent goroutine started.
- `react round started`: equivalent to Java `scheduleRound`.
- `model round stream requested`: model stream call is about to start.
- `model round stream completed`: equivalent to Java `processChunk` aggregation.
- `react round selected tool calls`: equivalent to Java `finishRound` choosing `TOOL_CALL`.
- `react round selected final answer`: equivalent to Java `finishRound` completing without tool calls.
- `tool batch started`: equivalent to Java `executeToolCalls`.
- `tool call started` / `tool call completed` / `tool call failed`: individual tool execution.
- `references emitted`: final search reference output.
- `complete event emitted`: final SSE complete event.

Model stream:

- `model stream request started`: HTTP request to the OpenAI-compatible endpoint was prepared.
- `model stream scanner opened`: `bufio.NewScanner(resp.Body)` started reading SSE lines.
- `model stream scanner completed`: `[DONE]` was received.
- `model stream consumer cancelled`: downstream channel send stopped because context was cancelled.
- `model stream chunk parse failed`: a `data:` payload could not be parsed as a stream chunk.
- `model stream scanner failed`: scanner returned an IO/read error.
- `model stream ended without done marker`: response body ended without `[DONE]`.

## Important Fields

Common fields:

- `request_id`
- `conversation_id`
- `round`
- `tool`
- `tool_call_id`
- `elapsed_ms`
- `message_count`
- `event_count`

Stream metrics:

- `line_count`: raw SSE lines read by `bufio.Scanner`.
- `data_line_count`: `data:` lines read from the model stream.
- `content_chunk_count`: stream chunks with model text content.
- `content_chars`: total streamed text size.
- `tool_delta_count`: streamed tool call deltas.
- `tool_call_count`: merged tool calls after a round.
- `model_http_headers_ms`: time spent until the model HTTP response headers are available.
- `first_model_chunk_ms`: time spent until the first non-empty model stream chunk is parsed.
- `first_chunk_ms`: time spent until the agent receives the first model chunk in a round.
- `first_text_ms`: time spent until the agent receives the first text chunk in a round.
- `first_tool_delta_ms`: time spent until the agent receives the first tool-call delta.

SSE metrics:

- `event_text_count`
- `event_thinking_count`
- `event_tool_start_count`
- `event_tool_end_count`
- `event_reference_count`
- `event_error_count`
- `event_complete_count`
- `first_event_ms`

Tool metrics:

- `args_chars`: tool argument JSON size.
- `args_summary`: short, safe argument summary such as `timezone=America/New_York`.
- `result_chars`: tool result payload size.
- `reference_count`: final collected search results.

Final-answer metrics:

- `final_answer_chars`
- `final_answer_preview`

## Why Raw Chunks Are Not Logged At Info Level

The original scanner path reads the model response like this:

```text
bufio.NewScanner(resp.Body)
-> scanner.Scan()
-> trim `data:`
-> parse model chunk
-> send chunk into channel
```

Logging every raw `data:` line makes the console noisy and can leak large model payloads. The Go replica therefore logs aggregate scanner counters and errors at the model boundary, while the agent layer logs round decisions and tool execution details.
