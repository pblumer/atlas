// Reference BPMN engine runner for the Atlas differential oracle.
// Reads a BPMN model (argv[2]), executes it on the independent bpmn-engine, and
// prints {"completed":[activity ids, sorted+unique], "ended":bool} as JSON.
//
// Scripts and sequence-flow conditions are inline JavaScript, evaluated in a vm
// sandbox: they only compute values. bpmn-engine's own logic decides all control
// flow (gateway routing, joins, branch selection) — that independence is the point.
const { Engine } = require('bpmn-engine');
const { EventEmitter } = require('events');
const fs = require('fs');
const vm = require('vm');

class Scripts {
  constructor() { this.compiled = {}; }
  register(element) {
    const { id, behaviour } = element;
    let body, language;
    if (behaviour.scriptFormat) {
      language = behaviour.scriptFormat;
      body = behaviour.script;
    } else if (behaviour.conditionExpression && behaviour.conditionExpression.language) {
      language = behaviour.conditionExpression.language;
      body = behaviour.conditionExpression.body;
    }
    if (language && /^javascript$/i.test(language) && body) {
      this.compiled[id] = new vm.Script(body, { filename: id });
    }
  }
  getScript(_language, element) {
    const script = this.compiled[element.id];
    if (!script) return null;
    return {
      execute(ctx, callback) {
        vm.createContext(ctx);
        ctx.next = callback;
        script.runInContext(ctx);
      },
    };
  }
}

const modelPath = process.argv[2];
if (!modelPath) { console.error('usage: node runner.js <model.bpmn>'); process.exit(2); }

const listener = new EventEmitter();
const completed = [];
listener.on('activity.end', (api) => completed.push(api.id));

const engine = Engine({ name: 'reference', source: fs.readFileSync(modelPath), scripts: new Scripts() });
engine.execute({ listener, variables: {} })
  .then(() => {
    const unique = [...new Set(completed)].sort();
    process.stdout.write(JSON.stringify({ completed: unique, ended: engine.state === 'idle' }));
  })
  .catch((err) => { console.error('reference error:', err && err.message); process.exit(1); });
