#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const configFile = join(homedir(), ".config", "linear", "config.json");

if (process.argv[2] === "--help") {
    console.log("Usage: states.js");
    console.log("\nLists all workflow states (statuses) with their IDs and names.");
    console.log("Use the state IDs with status.js to change issue status.");
    process.exit(0);
}

// Load API key
let config;
try {
    config = JSON.parse(readFileSync(configFile, "utf-8"));
} catch {
    console.error("✗ No API key found. Run: ./auth.js <api-key>");
    process.exit(1);
}

// Build GraphQL query
const query = `
  query WorkflowStates {
    workflowStates {
      nodes {
        id
        name
        type
        color
        team {
          id
          name
        }
      }
    }
  }
`;

// Make request
const response = await fetch("https://api.linear.app/graphql", {
    method: "POST",
    headers: {
        "Content-Type": "application/json",
        Authorization: config.apiKey,
    },
    body: JSON.stringify({ query }),
});

const result = await response.json();

if (result.errors) {
    console.error("✗ GraphQL errors:");
    result.errors.forEach((err) => console.error(err.message));
    process.exit(1);
}

const states = result.data.workflowStates.nodes;

if (states.length === 0) {
    console.log("No workflow states found");
    process.exit(0);
}

// Group by team
const byTeam = {};
for (const state of states) {
    const teamName = state.team.name;
    if (!byTeam[teamName]) {
        byTeam[teamName] = [];
    }
    byTeam[teamName].push(state);
}

// Output states grouped by team
for (const [teamName, teamStates] of Object.entries(byTeam)) {
    console.log(`\n${teamName}:`);
    for (const state of teamStates) {
        console.log(`  ${state.name} (${state.type})`);
        console.log(`    ID: ${state.id}`);
    }
}

console.log(`\nTotal: ${states.length} state${states.length === 1 ? "" : "s"} across ${Object.keys(byTeam).length} team${Object.keys(byTeam).length === 1 ? "" : "s"}`);
