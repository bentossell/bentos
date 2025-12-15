#!/usr/bin/env node

import { writeFileSync, mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const apiKey = process.argv[2];

if (!apiKey) {
    console.log("Usage: auth.js <api-key>");
    console.log("\nGet your API key from:");
    console.log("Linear Settings → API → Personal API keys");
    console.log("https://linear.app/settings/api");
    process.exit(1);
}

const configDir = join(homedir(), ".config", "linear");
const configFile = join(configDir, "config.json");

// Create directory if it doesn't exist
try {
    mkdirSync(configDir, { recursive: true });
} catch {}

// Save API key
writeFileSync(configFile, JSON.stringify({ apiKey }, null, 2));

console.log("✓ API key saved to", configFile);
console.log("\nTest it with: ./issues.js");
