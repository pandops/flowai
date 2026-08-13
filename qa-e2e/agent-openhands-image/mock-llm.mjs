import http from "node:http";

const port = Number(process.env.FLOWAI_MOCK_LLM_PORT || "19090");
let calls = 0;
let toolCalls = 0;

function toolResponse(body) {
  const tools = Array.isArray(body.tools) ? body.tools : [];
  const tool =
    tools.find(({ function: fn }) => /terminal|shell|bash/i.test(fn?.name)) ??
    tools.find(({ function: fn }) => /file|edit/i.test(fn?.name));
  if (!tool?.function?.name) return null;
  const properties = tool.function.parameters?.properties ?? {};
  const args = {};
  const command =
    "mkdir -p /workspace/project && printf flowai-real-runtime-ok > /workspace/project/flowai-real-runtime.marker";
  if ("command" in properties) args.command = command;
  else if ("commands" in properties) args.commands = [command];
  else if ("cmd" in properties) args.cmd = command;
  else return null;
  return {
    role: "assistant",
    content: null,
    tool_calls: [
      {
        id: "flowai-marker-tool-call",
        type: "function",
        function: { name: tool.function.name, arguments: JSON.stringify(args) },
      },
    ],
  };
}

function completion(body) {
  const tools = Array.isArray(body.tools) ? body.tools : [];
  if (tools.length > 0) toolCalls += 1;
  const finish = tools.find(({ function: fn }) => fn?.name === "finish");
  const message =
    toolCalls > 1 && finish
      ? {
          role: "assistant",
          content: null,
          tool_calls: [
            {
              id: "flowai-finish-tool-call",
              type: "function",
              function: {
                name: finish.function.name,
                arguments: JSON.stringify({
                  summary: "FlowAI marker written",
                  message: "Task complete.",
                }),
              },
            },
          ],
        }
      : (toolResponse(body) ?? {
          role: "assistant",
          content: "No supported marker-writing tool was supplied.",
        });
  return {
    id: `flowai-mock-${calls}`,
    object: "chat.completion",
    created: Math.floor(Date.now() / 1000),
    model: body.model ?? "flowai-mock",
    choices: [
      {
        index: 0,
        message,
        finish_reason: message.tool_calls ? "tool_calls" : "stop",
      },
    ],
    usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
  };
}

const server = http.createServer((request, response) => {
  if (request.method === "GET" && request.url === "/health") {
    response.writeHead(200, { "content-type": "application/json" });
    response.end('{"status":"ok"}');
    return;
  }
  if (
    request.method !== "POST" ||
    !request.url?.endsWith("/chat/completions")
  ) {
    response.writeHead(404);
    response.end();
    return;
  }
  let raw = "";
  request.on("data", (chunk) => {
    raw += chunk;
  });
  request.on("end", () => {
    calls += 1;
    const body = JSON.parse(raw);
    process.stdout.write(
      `${JSON.stringify({
        call: calls,
        model: body.model,
        tools: (body.tools ?? []).map(({ function: fn }) => ({
          name: fn?.name,
          properties: Object.keys(fn?.parameters?.properties ?? {}),
        })),
      })}\n`,
    );
    const result = completion(body);
    if (body.stream) {
      response.writeHead(200, {
        "content-type": "text/event-stream",
        "cache-control": "no-cache",
        connection: "keep-alive",
      });
      const choice = result.choices[0];
      response.write(
        `data: ${JSON.stringify({
          id: result.id,
          object: "chat.completion.chunk",
          created: result.created,
          model: result.model,
          choices: [
            {
              index: 0,
              delta: choice.message,
              finish_reason: choice.finish_reason,
            },
          ],
        })}\n\n`,
      );
      response.end("data: [DONE]\n\n");
      return;
    }
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify(result));
  });
});

server.listen(port, "0.0.0.0", () => {
  process.stdout.write(`flowai mock LLM listening on ${port}\n`);
});
