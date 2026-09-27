/** Copyright (c) 2024, Tegon, all rights reserved. **/

const js = require("@eslint/js");

const { baseConfig } = require("./base");

module.exports = [
  { ignores: ["dist/**"] },
  js.configs.recommended,
  ...baseConfig(),
  {
    // Libraries have no pages directory.
    rules: { "@next/next/no-html-link-for-pages": "off" },
  },
];
