import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import ts from 'typescript';

// Check syntax we can prove locally; dynamic text and layout still need browser review.
export function checkSource(source, filename) {
  const file = ts.createSourceFile(
    filename,
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const imports = new Map();
  for (const node of file.statements) {
    if (!ts.isImportDeclaration(node) || !ts.isStringLiteral(node.moduleSpecifier)) continue;
    const module = node.moduleSpecifier.text;
    const bindings = node.importClause?.namedBindings;
    if (bindings && ts.isNamedImports(bindings)) {
      for (const binding of bindings.elements) {
        imports.set(binding.name.text, {
          module,
          name: (binding.propertyName || binding.name).text,
        });
      }
    } else if (bindings && ts.isNamespaceImport(bindings)) {
      imports.set(bindings.name.text, { module, name: '*' });
    }
  }
  const imported = (name, module, exported) => {
    const [local, member] = name.split('.');
    const binding = imports.get(local);
    return (
      binding?.module === module &&
      (member ? binding.name === '*' && member === exported : binding.name === exported)
    );
  };
  const diagnostics = [];
  const report = (node, message) => {
    const { line, character } = file.getLineAndCharacterOfPosition(node.getStart(file));
    diagnostics.push(`${filename}:${line + 1}:${character + 1} ${message}`);
  };
  const meaningful = (node) => {
    if (!node) return false;
    if (ts.isJsxExpression(node)) return meaningful(node.expression);
    if (ts.isStringLiteral(node) || ts.isJsxText(node)) return !!node.text.trim();
    if (ts.isJsxElement(node) || ts.isJsxFragment(node)) return node.children.some(meaningful);
    if (ts.isJsxSelfClosingElement(node)) return false;
    return !['null', 'undefined', 'false', 'true'].includes(node.getText(file));
  };
  function visit(node) {
    if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
      const name = node.tagName.getText(file);
      const attributes = node.attributes.properties.filter(ts.isJsxAttribute);
      const attr = (key) => attributes.find((item) => item.name.getText(file) === key);
      const button = imported(name, 'antd', 'Button');
      if (name === 'button') report(node, '业务按钮使用 Ant Design Button，并提供图标和文案');
      if (button) {
        const icon = attr('icon')?.initializer;
        const iconValue = icon && ts.isJsxExpression(icon) ? icon.expression : icon;
        if (
          !iconValue ||
          ['null', 'undefined', 'false', 'true', "''", '""'].includes(iconValue.getText(file))
        ) {
          report(node, 'Button 必须提供非空 icon');
        }
        const children = ts.isJsxOpeningElement(node) ? node.parent.children : [];
        if (!children.some(meaningful) && !meaningful(attr('children')?.initializer)) {
          report(node, 'Button 必须提供可见文案，aria-label 不能替代文案');
        }
        const text = children
          .filter(ts.isJsxText)
          .map((child) => child.text)
          .join('')
          .trim();
        if (
          /^刷新(?:页面|列表|数据|状态)?$/.test(text) &&
          !filename.replaceAll('\\', '/').endsWith('/shared/admin-ui.tsx')
        ) {
          report(node, '刷新操作复用 RefreshButton');
        }
      }
      if (imported(name, '@ant-design/pro-components', 'QueryFilter')) {
        const value = attr('submitter')?.initializer;
        const expression = value && ts.isJsxExpression(value) ? value.expression : undefined;
        const binding =
          expression && ts.isCallExpression(expression)
            ? imports.get(expression.expression.getText(file))
            : undefined;
        if (
          !binding?.module.endsWith('/shared/admin-ui') ||
          binding.name !== 'queryActions' ||
          !expression.arguments.length
        ) {
          report(node, 'QueryFilter 使用 submitter={queryActions(loading)}');
        }
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(file);
  return diagnostics;
}

function sourceFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? sourceFiles(path) : entry.name.endsWith('.tsx') ? [path] : [];
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const root = join(dirname(fileURLToPath(import.meta.url)), '..');
  const files = sourceFiles(join(root, 'src'));
  const diagnostics = files.flatMap((file) =>
    checkSource(readFileSync(file, 'utf8'), relative(root, file)),
  );
  if (diagnostics.length) {
    console.error(diagnostics.join('\n'));
    process.exitCode = 1;
  } else {
    console.log(`UI 结构检查通过（${files.length} 个 TSX 文件）；交互和布局需浏览器验收。`);
  }
}
