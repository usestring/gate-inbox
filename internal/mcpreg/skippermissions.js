// Gate Inbox writes this file; edits to it do not last. An opencode session
// the board launched with skip permissions on loads it from its generated
// config, and it allows every permission request that session's opencode
// server would otherwise ask about, its subagents' included. A configured
// deny is final in opencode and never reaches the hook.
export default {
  id: "gate-inbox-skip-permissions",
  setup: async (ctx) => {
    await ctx.permission.hook("evaluate", (event) => {
      if (event.effect === "ask") event.effect = "allow";
    });
  },
};
