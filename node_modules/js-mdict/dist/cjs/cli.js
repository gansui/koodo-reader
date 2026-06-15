#!/usr/bin/env node
"use strict";
/* eslint-disable no-console */
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
//module/cli.js
const fs_1 = __importDefault(require("fs"));
const index_js_1 = require("./index.js");
function help() {
    console.log('js-mdict - A MDict dictionary file reader command line tool');
    console.log('Usage:');
    console.log('js-mdict path/xxx.mdx yourword');
    console.log('\nOr:');
    console.log('js-mdict path/xxx.mdd your-resource-key');
    console.log('\nIf you want output full result base64 string: ');
    console.log('js-mdict path/xxx.mdd your-resource-key -f');
}
/** Parse the command line */
const args = process.argv.slice(2);
// Validate input
if (args.length < 2) {
    console.log('Error: Requires 2 arguments');
    help();
    process.exit();
}
const src = args[0];
const target = args[1];
let full_out_flag = false;
if (args.length > 2) {
    if (args[2] == '-f') {
        full_out_flag = true;
    }
}
if (!fs_1.default.existsSync(src)) {
    console.log("Error: Source mdx/mdd file doesn't exist. given: ", src);
    help();
    process.exit();
}
const file_extionsion = src.split('.').pop();
if (file_extionsion !== 'mdx' && file_extionsion !== 'mdd') {
    console.log('Error: Source file must be a mdx or mdd file');
    help();
    process.exit();
}
let result = { keyText: '', definition: null };
switch (file_extionsion) {
    case 'mdx': {
        const mdx_dict = new index_js_1.MDX(src);
        const result = mdx_dict.lookup(target);
        if (result && result.definition) {
            console.log(result.definition);
        }
        else {
            console.log('not found');
        }
        break;
    }
    case 'mdd': {
        const mdd_dict = new index_js_1.MDD(src);
        result = mdd_dict.locate(target);
        if (result && result.definition) {
            if (!full_out_flag && result.definition.length > 100) {
                console.log(result.definition.slice(0, 100) + '...' + '(total: ' + result.definition.length / 1024 + ' KB)');
            }
            else {
                console.log(result.definition);
            }
        }
        else {
            console.log('not found');
        }
        break;
    }
    default: {
        console.log('Error: Source file must be a mdx or mdd file');
    }
}
//# sourceMappingURL=cli.js.map