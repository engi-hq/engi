// Example Engi plugin: reverses a string field.
// Run `npm run build` in sdk/typescript first: inside the SDK checkout the
// package resolves to itself. An installed plugin carries the SDK in its own
// node_modules (see the README, "Packaging a plugin").
import { definePlugin, NodeError } from "@engi/plugin-sdk";

const plugin = definePlugin({ name: "example-ts-text", version: "0.1.0" });

plugin.node(
  {
    type: "example.ts.reverse",
    displayName: "Reverse Text",
    group: "transform",
    description: "Reverses the text of a field of each item.",
    properties: [
      { name: "field", displayName: "Field", type: "string", default: "text", required: true,
        description: "Name of the string field to reverse." },
    ],
  },
  (ctx) =>
    [ctx.items().map((item, i) => {
      const field = ctx.param("field", i);
      const value = item.json[field];
      if (typeof value !== "string") throw new NodeError(`field "${field}" is not a string`, { itemIndex: i });
      return { json: { reversed: [...value].reverse().join("") }, pairedItem: [{ input: 0, item: i }] };
    })],
);

plugin.serve().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
