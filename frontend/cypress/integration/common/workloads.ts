import { And, Given, Then } from '@badeball/cypress-cucumber-preprocessor';
import { checkHealthIndicatorInTable, checkHealthStatusInTable, colExists } from './table';

function activateFilter(state: string): void {
  //decided to pause the refresh, because I'm intercepting the very same request that is used for the timed refresh

  cy.get('button#time_range_refresh-toggle').click();
  cy.get('button[id="0"]').click().get('#loading_kiali_spinner').should('not.exist');
  cy.intercept({
    pathname: '**/api/namespaces/bookinfo/workloads',
    query: {
      objects: ''
    }
  }).as('refresh');
  cy.get('select[aria-label="filter_select_value"]').select(state);
}

Given('a healthy workload in the cluster', function () {
  this.targetNamespace = 'bookinfo';
  this.targetWorkload = 'productpage-v1';
});

Given('an idle workload in the cluster', function () {
  this.targetNamespace = 'sleep';
  this.targetWorkload = 'sleep';

  cy.exec('kubectl scale -n sleep --replicas=0 deployment/sleep');
});

Given('a failing workload in the mesh', function () {
  this.targetNamespace = 'alpha';
  this.targetWorkload = 'v-server';
});

Given('a degraded workload in the mesh', function () {
  this.targetNamespace = 'alpha';
  this.targetWorkload = 'b-client';
});

And('user filters for workload type {string}', (workloadType: string) => {
  cy.get('select[aria-label="filter_select_type"]')
    .parent()
    .parent()
    .within(() => {
      cy.get('button').click();
      cy.get(`li[label="${workloadType}"]`).find('button').click();
    });
});

Then('user sees {string} in workloads table', (workload: string) => {
  cy.get('tbody').within(() => {
    if (workload === 'no workloads') {
      cy.contains('No workloads found');
    } else if (workload === 'workloads') {
      cy.contains('No workloads found').should('not.exist');
    } else {
      cy.contains('td', workload);
    }
  });
});

And('user should only see healthy workloads in workloads table', () => {
  cy.get('tbody').within(() => {
    cy.get('span[class*="icon-healthy"]').should('be.visible');
    cy.get('span[class*="icon-unhealthy"],span[class*="icon-degraded"],span[class*="icon-na"]').should('not.exist');
  });
});

And('user should only see workloads with the {string} label', (label: string) => {
  // TODO: purposefully breaking this to fail test.
  cy.wait('@refre');
  cy.get('tbody').within(() => {
    const regex = new RegExp(`\\b${label}=`);
    cy.get('tr').each($item => {
      cy.wrap($item)
        .find('td')
        .eq(4)
        .within(() => {
          // TODO: purposefully breaking this to fail test.
          cy.get('sn').children().contains(regex);
        });
    });
  });
});

And('user filters for version {string}', (state: string) => {
  activateFilter(state);
});

And('user filters for app label {string}', (state: string) => {
  activateFilter(state);
});

Then('the workload should be listed as {string}', function (healthStatus: string) {
  checkHealthIndicatorInTable(this.targetNamespace, 'Deployment', this.targetWorkload, healthStatus);
});

Then('the health status of the workload should be {string}', function (healthStatus: string) {
  checkHealthStatusInTable(this.targetNamespace, 'Deployment', this.targetWorkload, healthStatus);
});

Then('user sees all the Workloads toggles', () => {
  cy.get('[data-test="toggle-health"]').should('be.checked');
  cy.get('[data-test="toggle-istioResources"]').should('be.checked');
  colExists('Health', true);
  colExists('Details', true);
});
