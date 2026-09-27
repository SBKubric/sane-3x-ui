import * as fs from 'fs';
import * as path from 'path';
import { expect, test, type Project } from '@playwright/test';
import config from '../playwright.config';

/**
 * Every spec runs against one panel and one database (docker-compose.yml), in
 * parallel workers. Some of that panel's state is global, and a spec that
 * changes it races every spec that reads it: #150 was monitoring-page.spec.ts
 * asserting "no active edge" while chain-editor.spec.ts made one active.
 *
 * playwright.config.ts isolates each such resource with projects: every spec
 * that touches it sits in a project with one worker, and any two of those
 * projects are ordered by dependencies, so no two tests that touch it ever run
 * at the same time. This guard checks that from the specs' own sources, so a
 * new spec that drives the resource fails here until the config places it —
 * rather than flaking one run in ten on a release tag.
 */
const SHARED: { resource: string; touches: RegExp }[] = [
  // Hops and the active edge (proxy-chain.md §2.4), through the registry API
  // or by a hop joining on the sub server.
  { resource: 'chain registry', touches: /\/panel\/api\/chain\/|\/chain\/v1\// },
  // The one monEnable/monToken, through the settings form or `x-ui setting`.
  { resource: 'monitoring switch and token', touches: /monEnable|resetMonToken|mon-token/ },
];

const TEST_DIR = path.resolve(__dirname, '..', config.testDir || '.');
const projects = config.projects || [];

type Pattern = Project['testMatch'];

/** Whether a project's testMatch/testIgnore picks a file; `unset` is what an absent option means. */
function picks(pattern: Pattern, file: string, unset: boolean): boolean {
  if (pattern === undefined) return unset;
  const list = Array.isArray(pattern) ? pattern : [pattern];
  return list.some((p) => {
    // Playwright reads a string as a glob; the config uses only RegExps, and
    // guessing at glob semantics here would make the guard lie.
    if (!(p instanceof RegExp)) throw new Error(`the shared-state guard reads RegExp patterns only, got ${String(p)}`);
    return p.test(file);
  });
}

function runs(project: Project, file: string): boolean {
  return picks(project.testMatch, file, true) && !picks(project.testIgnore, file, false);
}

/** Every project a project waits for, directly or through another. */
function waitsFor(name: string, seen = new Set<string>()): Set<string> {
  const project = projects.find((p) => p.name === name);
  for (const dep of project?.dependencies || []) {
    if (!seen.has(dep)) {
      seen.add(dep);
      waitsFor(dep, seen);
    }
  }
  return seen;
}

const self = path.basename(__filename);
const specs = fs
  .readdirSync(TEST_DIR)
  .filter((f) => f.endsWith('.spec.ts') && f !== self)
  .map((f) => ({ file: path.join(TEST_DIR, f), source: fs.readFileSync(path.join(TEST_DIR, f), 'utf8') }));

test.describe('shared panel state', () => {
  for (const { resource, touches } of SHARED) {
    test(`specs that touch the ${resource} never run beside each other`, () => {
      const touching = specs.filter((s) => touches.test(s.source));
      // A pattern that matches nothing guards nothing.
      expect(touching.length).toBeGreaterThan(1);

      const problems: string[] = [];
      const owners = new Set<string>();
      for (const { file } of touching) {
        const name = path.basename(file);
        const hosts = projects.filter((p) => runs(p, file));
        if (hosts.length !== 1) {
          problems.push(`${name} runs in ${hosts.length} projects (${hosts.map((p) => p.name).join(', ')}), not one`);
          continue;
        }
        const project = hosts[0];
        if (project.workers !== 1) problems.push(`${name} runs in "${project.name}", which has more than one worker`);
        owners.add(project.name || '');
      }
      for (const a of owners) {
        for (const b of owners) {
          if (a < b && !waitsFor(a).has(b) && !waitsFor(b).has(a)) {
            problems.push(`projects "${a}" and "${b}" both touch it and neither waits for the other`);
          }
        }
      }
      expect(problems).toEqual([]);
    });
  }
});
