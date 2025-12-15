#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const configFile = join(homedir(), ".config", "linear", "config.json");

// Parse arguments
const args = process.argv.slice(2);

if (args[0] === "--help") {
    console.log("Usage:");
    console.log("  team.js                          # List all teams");
    console.log("  team.js <issue-id> <team-id>     # Move issue to team");
    console.log("\nExamples:");
    console.log("  team.js                          # Show all teams");
    console.log("  team.js OPS-1212 <team-id>      # Move issue");
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

// If no arguments, list teams
if (args.length === 0) {
    const query = `
      query Teams {
        teams {
          nodes {
            id
            key
            name
          }
        }
      }
    `;

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

    const teams = result.data.teams.nodes;

    console.log("Available Teams:\n");
    for (const team of teams) {
        console.log(`${team.key} - ${team.name}`);
        console.log(`  ID: ${team.id}\n`);
    }
    process.exit(0);
}

// Move issue to team
const [issueId, teamId] = args;

if (!issueId || !teamId) {
    console.log("Usage: team.js <issue-id> <team-id>");
    console.log("\nRun 'team.js' with no arguments to list teams");
    process.exit(1);
}

const mutation = `
  mutation IssueUpdate($id: String!, $teamId: String!) {
    issueUpdate(
      id: $id,
      input: {
        teamId: $teamId
      }
    ) {
      success
      issue {
        id
        identifier
        title
        team {
          id
          key
          name
        }
      }
    }
  }
`;

const variables = {
    id: issueId,
    teamId: teamId,
};

const response = await fetch("https://api.linear.app/graphql", {
    method: "POST",
    headers: {
        "Content-Type": "application/json",
        Authorization: config.apiKey,
    },
    body: JSON.stringify({ query: mutation, variables }),
});

const result = await response.json();

if (result.errors) {
    console.error("✗ GraphQL errors:");
    result.errors.forEach((err) => console.error(err.message));
    process.exit(1);
}

if (!result.data?.issueUpdate) {
    console.error("✗ Unexpected response format");
    console.error(JSON.stringify(result, null, 2));
    process.exit(1);
}

const { success, issue } = result.data.issueUpdate;

if (success) {
    console.log(`✓ Updated ${issue.identifier}: ${issue.title}`);
    console.log(`  New team: ${issue.team.name} (${issue.team.key})`);
} else {
    console.error("✗ Update failed");
    process.exit(1);
}
