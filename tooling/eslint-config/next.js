/** Copyright (c) 2024, Tegon, all rights reserved. **/

const { baseConfig } = require("./base");

const after = (pattern) => ({ pattern, group: "internal", position: "after" });

module.exports = [
  {
    ignores: ["src/@@generated/**", "**/*.js", ".next/**", ".next-*/**"],
  },
  ...baseConfig({
    pathGroups: [
      after("+(modules){/**,}"),
      after("+(common|wrappers|layouts){/**,}"),
      after("+(icons|components|hooks){/**,}"),
      after("+(services){/**,}"),
      after("+(store){/**,}"),
    ],
    typescriptRules: { "@typescript-eslint/no-explicit-any": "warn" },
  }),
];
