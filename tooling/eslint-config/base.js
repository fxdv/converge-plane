/** Copyright (c) 2024, Tegon, all rights reserved. **/

const nextCoreWebVitals = require("eslint-config-next/core-web-vitals");
const turbo = require("eslint-config-turbo/flat").default;
const prettierRecommended = require("eslint-plugin-prettier/recommended");
const unusedImports = require("eslint-plugin-unused-imports");

// eslint-config-next registers the react, react-hooks, import, jsx-a11y
// and @next/next plugins for every source file, and @typescript-eslint
// for .ts/.tsx only. A flat config cannot register a plugin name twice,
// so these presets add only what next leaves out.
function baseConfig({ pathGroups = [], typescriptRules = {} } = {}) {
  return [
    ...nextCoreWebVitals,
    ...turbo,
    prettierRecommended,
    {
      name: "converge/base",
      plugins: { "unused-imports": unusedImports },
      rules: {
        curly: "warn",
        eqeqeq: "error",
        "prettier/prettier": "warn",
        "unused-imports/no-unused-imports": "warn",
        "no-else-return": "warn",
        "no-lonely-if": "warn",
        "no-inner-declarations": "off",
        "no-unused-vars": "off",
        "no-useless-computed-key": "warn",
        "no-useless-return": "warn",
        "no-var": "warn",
        "object-shorthand": ["warn", "always"],
        "prefer-arrow-callback": "warn",
        "prefer-const": "warn",
        "prefer-destructuring": [
          "warn",
          { AssignmentExpression: { array: true } },
        ],
        "prefer-object-spread": "warn",
        "prefer-template": "warn",
        "spaced-comment": ["warn", "always", { markers: ["/"] }],
        yoda: "warn",
        "import/order": [
          "warn",
          {
            "newlines-between": "always",
            groups: [
              "type",
              "builtin",
              "external",
              "internal",
              ["parent", "sibling"],
              "index",
            ],
            pathGroupsExcludedImportTypes: ["builtin"],
            pathGroups,
            alphabetize: { order: "asc", caseInsensitive: true },
          },
        ],
        // React Compiler rules from react-hooks 7. The app doesn't run the
        // compiler, and these patterns predate the rules.
        "react-hooks/immutability": "warn",
        "react-hooks/refs": "warn",
        "react-hooks/set-state-in-effect": "warn",
        "react-hooks/static-components": "warn",
        "react-hooks/use-memo": "warn",
      },
    },
    {
      name: "converge/typescript",
      files: ["**/*.ts", "**/*.tsx"],
      rules: {
        "@typescript-eslint/array-type": ["warn", { default: "array-simple" }],
        "@typescript-eslint/ban-ts-comment": [
          "warn",
          { "ts-expect-error": "allow-with-description" },
        ],
        // typescript-eslint 8 split ban-types into these three.
        "@typescript-eslint/no-empty-object-type": "warn",
        "@typescript-eslint/no-unsafe-function-type": "warn",
        "@typescript-eslint/no-wrapper-object-types": "warn",
        "@typescript-eslint/consistent-indexed-object-style": [
          "warn",
          "record",
        ],
        "@typescript-eslint/consistent-type-definitions": [
          "warn",
          "interface",
        ],
        "@typescript-eslint/no-unused-vars": "warn",
        ...typescriptRules,
      },
    },
  ];
}

module.exports = { baseConfig };
