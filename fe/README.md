# Cheap Dish Map — Frontend (Angular)

Talks to the Go backend (see the `cheapdish` project) at `http://localhost:8080`
by default — change `API_BASE` in `src/app/dish.service.ts` if your backend
runs elsewhere.

**Setup:**
```bash
npm install
ng serve
```
Then open `http://localhost:4200`. Make sure the backend is running first
(`go run .` in the `cheapdish` folder) — the app needs it to load the list
of dishes and search results.

This project was generated with [Angular CLI](https://github.com/angular/angular-cli) version 18.2.21.

## Development server

Run `ng serve` for a dev server. Navigate to `http://localhost:4200/`. The application will automatically reload if you change any of the source files.

## Code scaffolding

Run `ng generate component component-name` to generate a new component. You can also use `ng generate directive|pipe|service|class|guard|interface|enum|module`.

## Build

Run `ng build` to build the project. The build artifacts will be stored in the `dist/` directory.

## Running unit tests

Run `ng test` to execute the unit tests via [Karma](https://karma-runner.github.io).

## Running end-to-end tests

Run `ng e2e` to execute the end-to-end tests via a platform of your choice. To use this command, you need to first add a package that implements end-to-end testing capabilities.

## Further help

To get more help on the Angular CLI use `ng help` or go check out the [Angular CLI Overview and Command Reference](https://angular.dev/tools/cli) page.
