// Serves a copy of the TypeScript example's reverse node over HTTP on a free port
// and prints the port. (The example's main.mjs starts the stdio transport on
// import, so it cannot be reused here.)
import { definePlugin } from "../../../../../../sdk/typescript/dist/index.js";

const plugin = definePlugin({ name: "example-ts-text", version: "0.1.0" });

plugin.node(
  {
    type: "example.ts.reverse",
    displayName: "Reverse Text",
    group: "transform",
    properties: [{ name: "field", displayName: "Field", type: "string", default: "text", required: true }],
  },
  (ctx) => [ctx.items().map((item, i) => ({
    json: { reversed: [...item.json[ctx.param("field", i)]].reverse().join("") },
    pairedItem: [{ input: 0, item: i }],
  }))],
);

const server = await plugin.serveHttp({ port: 0, host: "127.0.0.1" });
console.log(server.address().port);
