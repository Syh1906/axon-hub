import * as jsx from 'react/jsx-runtime';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

function load(path, dependencies) {
  const source = ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023, jsx: ts.JsxEmit.ReactJSX },
  }).outputText;
  const exports = {};
  new Function('require', 'exports', source)((name) => {
    assert.ok(name in dependencies, `Unexpected dependency: ${name}`);
    return dependencies[name];
  }, exports);
  return exports;
}

const utils = { extractNumberIDAsNumber: (id) => Number(id.split('/').at(-1)), cn: (...values) => values.filter(Boolean).join(' ') };
const options = load('../utils/channel-select-options.ts', { '@/lib/utils': utils });
const channels = [
  { id: 'gid://axonhub/Channel/101', name: 'same', tags: ['first'], type: 'openai' },
  { id: 'gid://axonhub/Channel/102', name: 'same', tags: ['second'], type: 'openai' },
  { id: 'gid://axonhub/Channel/103', name: 'other', tags: [] },
];

test('同名标签可辨认，改名和标签变化不改变选项 ID', () => {
  const before = options.buildChannelSelectOptions(channels);
  assert.deepEqual(
    before.map((item) => item.id),
    [101, 102, 103]
  );
  assert.equal(before[0].label, 'same · first · openai · #101');
  assert.equal(before[1].label, 'same · second · openai · #102');
  assert.equal(before[2].label, 'other');
  const after = options.buildChannelSelectOptions(channels.map((channel) => ({ ...channel, name: 'renamed', tags: ['changed'] })));
  assert.deepEqual(
    after.map((item) => item.id),
    [101, 102, 103]
  );
  assert.equal(new Set(after.map((item) => item.label)).size, 3);
});

const components = (names) => Object.fromEntries(names.map((name) => [name, name]));
const { ChannelMultiSelect } = load('./channel-multi-select.tsx', {
  react: { useMemo: (fn) => fn(), useRef: () => ({ current: null }) },
  'react/jsx-runtime': jsx,
  'react-i18next': {
    useTranslation: () => ({ t: (key, values) => (values?.name ? `remove ${values.name}` : (values?.defaultValue ?? key)) }),
  },
  'lucide-react': components(['Check', 'ChevronsUpDown', 'X']),
  '@/lib/utils': utils,
  '@/components/ui/badge': components(['Badge']),
  '@/components/ui/button': components(['Button']),
  '@/components/ui/command': components(['Command', 'CommandEmpty', 'CommandGroup', 'CommandInput', 'CommandItem', 'CommandList']),
  '@/components/ui/popover': components(['Popover', 'PopoverContent', 'PopoverTrigger']),
  '../utils/channel-select-options': options,
});

function elements(tree) {
  if (!tree || typeof tree !== 'object') return [];
  if (Array.isArray(tree)) return tree.flatMap(elements);
  return [tree, ...elements(tree.props?.children)];
}

test('真实选择和移除回调保持第二条同名渠道及未加载 ID', () => {
  let value = [102, 999];
  const render = () =>
    elements(
      ChannelMultiSelect({
        value,
        channels,
        onChange: (next) => {
          value = next;
        },
        placeholder: 'channels',
      })
    );
  render()
    .find((item) => item.type === 'CommandItem' && item.props.value === '103')
    .props.onSelect();
  assert.deepEqual(value, [102, 999, 103]);
  render()
    .find((item) => item.type === 'CommandItem' && item.props.value === '101')
    .props.onSelect();
  assert.deepEqual(value, [102, 999, 103, 101]);
  render()
    .find((item) => item.type === 'button' && item.props['aria-label'].includes('#102'))
    .props.onClick();
  assert.deepEqual(value, [999, 103, 101]);
  render()
    .find((item) => item.type === 'CommandItem' && item.props.value === '101')
    .props.onSelect();
  assert.deepEqual(value, [999, 103]);
});

for (const path of [
  '../../apikeys/components/apikeys-profiles-dialog.tsx',
  '../../apikeys/components/apikeys-create-template-dialog.tsx',
  '../../apikeys/components/apikeys-edit-template-dialog.tsx',
  '../../projects/components/project-profiles-dialog.tsx',
  '../../models/components/models-association-dialog.tsx',
]) {
  test(`实际表单绑定传递 ID：${path}`, () => {
    const source = ts.createSourceFile(
      path,
      readFileSync(new URL(path, import.meta.url), 'utf8'),
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TSX
    );
    let props;
    function visit(node) {
      if (ts.isJsxSelfClosingElement(node) && node.tagName.getText(source) === 'ChannelMultiSelect') {
        props = Object.fromEntries(
          node.attributes.properties
            .filter(ts.isJsxAttribute)
            .map((item) => [item.name.getText(source), item.initializer.expression.getText(source)])
        );
      }
      ts.forEachChild(node, visit);
    }
    visit(source);
    assert.ok(props);
    let saved;
    const field = {
      value: [102],
      onChange: (value) => {
        saved = value;
      },
    };
    const binding = new Function('field', `return {value:${props.value},onChange:${props.onChange}}`)(field);
    binding.onChange([...binding.value, 103]);
    assert.deepEqual(saved, [102, 103]);
  });
}
