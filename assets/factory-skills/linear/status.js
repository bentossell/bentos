#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const configFile = join(homedir(), ".config", "linear", "config.json");

const issueId = process.argv[2];
const stateId = process.argv[3];

if (!issueId || !stateId) {
    console.log("Usage: status.js <issue-id> <state-id>");
    console.log("\nExamples:");
    console.log("  status.js BLA-123 <state-id>");
    console.log("  status.js <full-uuid> <state-id>");
    console.log("\nUse states.js to find state IDs");
    process.exit(1);
}

// Load API key
let config;
try {
    config = JSON.parse(readFileSync(configFile, "utf-8"));
} catch {
    console.error("✗ No API key found. Run: ./auth.js <api-key>");
    process.exit(1);
}

// Build GraphQL mutation
const mutation = `
  mutation IssueUpdate($id: String!, $stateId: String!) {
    issueUpdate(
      id: $id,
      input: {
        stateId: $stateId
      }
    ) {
      success
      issue {
        id
        identifier
        title
        state {
          id
          name
          type
        }
      }
    }
  }
`;

const variables = {
    id: issueId,
    stateId: stateId,
};

// Make request
const response = await fetch("https://api.linear.app/graphql", {
    method: "POST",
    headers: {
        "Content-Type": "application/json",
        Authorization: config.apiKey,
    },
    body: JSON.stringify({ mutation, variables }),
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
    console.log(`  New status: ${issue.state.name} (${issue.state.type})`);
} else {
    console.error("✗ Update failed");
    process.exit(1);
}
