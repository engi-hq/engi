// Conformance test plugin: calls the host's expression.eval for every item.
// testdata/ts -> repository root is six levels up.
import { definePlugin } from "../../../../../../sdk/typescript/dist/index.js";

const plugin = definePlugin({ name: "conformance-ts-eval", version: "0.1.0" });

plugin.node(
  { type: "conformance.ts.eval", displayName: "Eval", group: "transform" },
  async (ctx) => (ctx.log.info("evaluating", { step: "eval" }), [await Promise.all(ctx.items().map(async (_, i) => ({ json: { value: await ctx.eval("1+1", i) } })))]),
);

plugin.serve().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
