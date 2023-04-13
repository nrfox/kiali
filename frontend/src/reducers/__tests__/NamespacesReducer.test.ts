import namespaceState from '../NamespaceState';
import { GlobalActions } from '../../actions/GlobalActions';
import { NamespaceActions } from '../../actions/NamespaceAction';

describe('Namespaces reducer', () => {
  it('should return the initial state', () => {
    expect(namespaceState(undefined, GlobalActions.unknown())).toEqual({
      isFetching: false,
      activeNamespaces: [],
      items: [],
      lastUpdated: undefined,
      filter: ''
    });
  });

  it('should handle ACTIVE_NAMESPACES', () => {
    const currentState = {
      activeNamespaces: [{ name: 'my-namespace', cluster: 'east' }],
      isFetching: false,
      items: [],
      lastUpdated: undefined,
      filter: ''
    };
    const requestStartedAction = NamespaceActions.setActiveNamespaces([{ name: 'istio', cluster: 'east' }]);
    const expectedState = {
      activeNamespaces: [{ name: 'istio', cluster: 'east' }],
      isFetching: false,
      items: [],
      lastUpdated: undefined,
      filter: ''
    };
    expect(namespaceState(currentState, requestStartedAction)).toEqual(expectedState);
  });

  it('should handle SET_FILTER', () => {
    const currentState = {
      activeNamespaces: [{ name: 'my-namespace', cluster: 'east' }],
      isFetching: false,
      items: [],
      lastUpdated: undefined,
      filter: ''
    };
    const requestStartedAction = NamespaceActions.setFilter('istio');
    const expectedState = {
      activeNamespaces: [{ name: 'my-namespace', cluster: 'east' }],
      isFetching: false,
      items: [],
      lastUpdated: undefined,
      filter: 'istio'
    };
    expect(namespaceState(currentState, requestStartedAction)).toEqual(expectedState);
  });

  it('should handle TOGGLE_NAMESPACE to remove a namespace', () => {
    const currentState = {
      activeNamespaces: [
        { name: 'my-namespace', cluster: 'east' },
        { name: 'my-namespace-2', cluster: 'west' }
      ],
      isFetching: false,
      items: [],
      lastUpdated: undefined,
      filter: ''
    };
    const requestStartedAction = NamespaceActions.toggleActiveNamespace({ name: 'my-namespace', cluster: 'east' });
    const expectedState = {
      activeNamespaces: [{ name: 'my-namespace-2', cluster: 'west' }],
      isFetching: false,
      items: [],
      lastUpdated: undefined,
      filter: ''
    };
    expect(namespaceState(currentState, requestStartedAction)).toEqual(expectedState);
  });

  it('should handle TOGGLE_NAMESPACE to add a namespace', () => {
    const currentState = {
      activeNamespaces: [
        { name: 'my-namespace', cluster: 'east' },
        { name: 'my-namespace-2', cluster: 'west' }
      ],
      isFetching: false,
      items: [],
      lastUpdated: undefined,
      filter: ''
    };
    const requestStartedAction = NamespaceActions.toggleActiveNamespace({ name: 'my-namespace-3', cluster: 'east' });
    const expectedState = {
      activeNamespaces: [
        { name: 'my-namespace', cluster: 'east' },
        { name: 'my-namespace-2', cluster: 'west' },
        { name: 'my-namespace-3', cluster: 'east' }
      ],
      isFetching: false,
      items: [],
      lastUpdated: undefined,
      filter: ''
    };
    expect(namespaceState(currentState, requestStartedAction)).toEqual(expectedState);
  });

  it('should handle NAMESPACE_REQUEST_STARTED', () => {
    const currentState = {
      activeNamespaces: [{ name: 'my-namespace', cluster: 'east' }],
      isFetching: false,
      items: [],
      lastUpdated: undefined,
      filter: ''
    };
    const requestStartedAction = NamespaceActions.requestStarted();
    const expectedState = {
      activeNamespaces: [{ name: 'my-namespace', cluster: 'east' }],
      isFetching: true,
      items: [],
      lastUpdated: undefined,
      filter: ''
    };
    expect(namespaceState(currentState, requestStartedAction)).toEqual(expectedState);
  });

  it('should handle NAMESPACE_FAILED', () => {
    const currentState = {
      activeNamespaces: [{ name: 'my-namespace', cluster: 'east' }],
      isFetching: true,
      items: [],
      filter: ''
    };
    const requestStartedAction = NamespaceActions.requestFailed();
    const expectedState = {
      activeNamespaces: [{ name: 'my-namespace', cluster: 'east' }],
      isFetching: false,
      items: [],
      filter: ''
    };
    expect(namespaceState(currentState, requestStartedAction)).toEqual(expectedState);
  });

  it('should handle NAMESPACE_SUCCESS', () => {
    const currentDate = new Date();
    const currentState = {
      activeNamespaces: [{ name: 'my-namespace', cluster: 'east' }],
      isFetching: true,
      items: [
        { name: 'old', cluster: 'east' },
        { name: 'my-namespace', cluster: 'east' }
      ],
      lastUpdated: undefined,
      filter: ''
    };
    const requestStartedAction = NamespaceActions.receiveList(
      [
        { name: 'a', cluster: 'west' },
        { name: 'b', cluster: 'west' },
        { name: 'c', cluster: 'west' }
      ],
      currentDate
    );
    const expectedState = {
      activeNamespaces: [],
      isFetching: false,
      items: [
        { name: 'a', cluster: 'west' },
        { name: 'b', cluster: 'west' },
        { name: 'c', cluster: 'west' }
      ],
      lastUpdated: currentDate,
      filter: ''
    };
    expect(namespaceState(currentState, requestStartedAction)).toEqual(expectedState);
  });
});
