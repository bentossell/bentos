#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const configFile = join(homedir(), ".config", "linear", "config.json");

// Parse arguments
let teamId = null;
let assigneeEmail = null;
let limit = 50;
let issueId = null;

for (let i = 2; i < process.argv.length; i++) {
    if (process.argv[i] === "--team" && process.argv[i + 1]) {
        teamId = process.argv[i + 1];
        i++;
    } else if (process.argv[i] === "--assignee" && process.argv[i + 1]) {
        assigneeEmail = process.argv[i + 1];
        i++;
    } else if (process.argv[i] === "--limit" && process.argv[i + 1]) {
        limit = parseInt(process.argv[i + 1], 10);
        i++;
    } else if (process.argv[i] === "--id" && process.argv[i + 1]) {
        issueId = process.argv[i + 1];
        i++;
    } else if (process.argv[i] === "--help") {
        console.log("Usage: issues.js [--team TEAM-ID] [--assignee EMAIL] [--limit N] [--id ISSUE-ID]");
        console.log("\nExamples:");
        console.log("  issues.js                          # List all issues");
        console.log("  issues.js --team TEAM-ID           # Filter by team");
        console.log("  issues.js --assignee me            # Filter by current user");
        console.log("  issues.js --assignee user@email.com # Filter by assignee email");
        console.log("  issues.js --limit 20               # Limit results");
        console.log("  issues.js --id DR-20               # Fetch single issue by identifier");
        process.exit(0);
    } else if (i === 2 && !process.argv[i].startsWith("--")) {
        // Support positional argument for issue ID
        issueId = process.argv[i];
    }
}

// Load API key
let config;
try {
    config = JSON.parse(readFileSync(configFile, "utf-8"));
} catch {
    console.error("✗ No API key found. Run: ./auth.js <api-key>");
    process.exit(1);
}

// Get current user ID if needed
let assigneeId = null;
if (assigneeEmail) {
    if (assigneeEmail === "me") {
        // Query for current user
        const userQuery = `
          query {
            viewer {
              id
              email
            }
          }
        `;
        const userResponse = await fetch("https://api.linear.app/graphql", {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                Authorization: config.apiKey,
            },
            body: JSON.stringify({ query: userQuery }),
        });
        const userResult = await userResponse.json();
        if (userResult.errors) {
            console.error("✗ Failed to fetch current user:");
            userResult.errors.forEach((err) => console.error(err.message));
            process.exit(1);
        }
        assigneeId = userResult.data.viewer.id;
    } else {
        // Query for user by email
        const userQuery = `
          query Users {
            users {
              nodes {
                id
                email
              }
            }
          }
        `;
        const userResponse = await fetch("https://api.linear.app/graphql", {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                Authorization: config.apiKey,
            },
            body: JSON.stringify({ query: userQuery }),
        });
        const userResult = await userResponse.json();
        if (userResult.errors) {
            console.error("✗ Failed to fetch users:");
            userResult.errors.forEach((err) => console.error(err.message));
            process.exit(1);
        }
        const user = userResult.data.users.nodes.find(u => u.email === assigneeEmail);
        if (!user) {
            console.error(`✗ User with email ${assigneeEmail} not found`);
            process.exit(1);
        }
        assigneeId = user.id;
    }
}

// Build GraphQL query
let query;
let variables;

if (issueId) {
    // Query for a single issue by identifier
    query = `
      query Issue($id: String!) {
        issue(id: $id) {
          id
          identifier
          title
          description
          state {
            id
            name
            type
          }
          team {
            id
            name
          }
          assignee {
            id
            name
          }
          createdAt
          updatedAt
        }
      }
    `;
    variables = { id: issueId };
} else {
    // Query for multiple issues
    query = `
      query Issues($first: Int, $filter: IssueFilter) {
        issues(first: $first, filter: $filter) {
          nodes {
            id
            identifier
            title
            description
            state {
              id
              name
              type
            }
            team {
              id
              name
            }
            assignee {
              id
              name
            }
            createdAt
            updatedAt
          }
        }
      }
    `;
    
    // Build filter
    let filter = {};
    if (teamId) {
        filter.team = { id: { eq: teamId } };
    }
    if (assigneeId) {
        filter.assignee = { id: { eq: assigneeId } };
    }
    
    variables = {
        first: limit,
        filter: Object.keys(filter).length > 0 ? filter : undefined,
    };
}

// Make request
const response = await fetch("https://api.linear.app/graphql", {
    method: "POST",
    headers: {
        "Content-Type": "application/json",
        Authorization: config.apiKey,
    },
    body: JSON.stringify({ query, variables }),
});

const result = await response.json();

if (result.errors) {
    console.error("✗ GraphQL errors:");
    result.errors.forEach((err) => console.error(err.message));
    process.exit(1);
}

// Handle single issue vs multiple issues
let issues;
if (issueId) {
    if (!result.data.issue) {
        console.log(`Issue ${issueId} not found`);
        process.exit(0);
    }
    issues = [result.data.issue];
} else {
    issues = result.data.issues.nodes;
    if (issues.length === 0) {
        console.log("No issues found");
        process.exit(0);
    }
}

// Output issues
for (const issue of issues) {
    console.log(`${issue.identifier} - ${issue.title}`);
    console.log(`  Status: ${issue.state.name} (${issue.state.type})`);
    console.log(`  Team: ${issue.team.name}`);
    if (issue.assignee) {
        console.log(`  Assignee: ${issue.assignee.name}`);
    }
    if (issue.description) {
        const preview = issue.description.slice(0, 100).replace(/\n/g, " ");
        console.log(`  Description: ${preview}${issue.description.length > 100 ? "..." : ""}`);
    }
    console.log(`  ID: ${issue.id}`);
    console.log(`  State ID: ${issue.state.id}`);
    console.log("");
}

console.log(`Total: ${issues.length} issue${issues.length === 1 ? "" : "s"}`);
