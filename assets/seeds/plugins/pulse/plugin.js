// pulse — a live plugin that proves the organism's nervous system.
// It reacts to bus events in real time and keeps its own heartbeat.
// This file is plain JS in your genome: edit it here, or ask EVOLV to
// rewrite it via evolve_plugin. Events: evolution, agent:token, fs:change…

evolv.on("evolution", (e) => {
  evolv.log("saw a mutation:", e.kind || "?", e.name || "");
});

let beats = 0;
evolv.every(30000, () => {
  beats++;
  evolv.log("heartbeat #" + beats + " — still alive");
});
