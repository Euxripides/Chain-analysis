#!/usr/bin/env node
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
const require = createRequire(new URL('../package.json', import.meta.url));
const wanted = process.env.CA_SOLC_VERSION || '';
if (!/^0\.\d+\.\d+(?:\+[a-zA-Z0-9.]+)?$/.test(wanted)) throw new Error('COMPILER_IDENTITY_REQUIRED');
const compiler = require(`solc-${wanted.split('+')[0]}`);
if (!compiler.version().startsWith(wanted)) throw new Error('COMPILER_IDENTITY_MISMATCH');
if (process.argv.includes('--version')) {
  process.stdout.write(`solc, the solidity compiler commandline interface\nVersion: ${compiler.version()}\n`);
} else if (process.argv.includes('--standard-json')) {
  const input = readFileSync(0, 'utf8');
  if (Buffer.byteLength(input) > 24 << 20) throw new Error('COMPILER_INPUT_TOO_LARGE');
  const compile = compiler.compileStandardWrapper || compiler.compile;
  process.stdout.write(compile.call(compiler, input));
} else throw new Error('COMPILER_ARGUMENT_UNSUPPORTED');
