import { PluginDispatcher } from './dispatcher';
import { GenericSplTokenPlugin } from './builtin/generic_spl_token';
import { GenericSystemPlugin } from './builtin/generic_system';

export function createDefaultDispatcher(): PluginDispatcher {
  return new PluginDispatcher([
    new GenericSplTokenPlugin(),
    new GenericSystemPlugin(),
  ]);
}
