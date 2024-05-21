// run the scalar/cli bundle with an output of tmp/fapi.env.json
// after we've dummped both fapi and bapi into tmp files
// we run the scalar/cli share with the appropriate token

// over on clerk/clerk we have 2 templates setup that are configured
// to consume the url of the appropriate scalar/cli share URLs that get returned

import { exec } from 'child_process';

const fapi = {
  development: 'b300374ad64d214f94ea39c46b655fd4',
  production: '6f4312d7046c052b25ac950fb676f6b3'
}
// fapi dev: https://sandbox.scalar.com/p/YV9vj
// fapi prod: https://sandbox.scalar.com/p/ABgG8

const bapi = {
  development: '62cc0e163330aadf9a5ed3a6c61bdafc',
  production: '52a68cf854784e5301e6f24cb4b6666b'
}

// bapi dev: https://sandbox.scalar.com/p/9cl58
// bapi prod: https://sandbox.scalar.com/p/29z0-

const APIS = [fapi, bapi];

const currentEnv = process.env.NODE_ENV || 'development';

APIS.forEach(api => {
  const fapiToken = api[currentEnv];
  const bapiToken = api[currentEnv];

  if(currentEnv === 'development'){
    if(api === fapi){
      exec(`npx swagger-cli bundle -r ./api/fapi/openapi/versions/2021-02-05.yml -o ./tmp/fapi.env.json && npx @scalar/cli share --token=${fapiToken} ./api/fapi/openapi/versions/2021-02-05.yml`, (error, stdout, stderr) => {
        if (error) {
          console.error(`FAPI Dev Error: ${error.message}`);
          return;
        }
        if (stderr) {
          console.error(`FAPI Dev Stderr: ${stderr}`);
          return;
        }

        console.log(`FAPI Dev Stdout: ${stdout}`);
      });
    }
    if(api === bapi){
      exec(`npx swagger-cli bundle -r ./api/bapi/openapi/versions/2021-02-05.yml -o ./tmp/bapi.env.json && npx @scalar/cli share --token=${bapiToken} ./api/bapi/openapi/versions/2021-02-05.yml`, (error, stdout, stderr) => {
        if (error) {
          console.error(`BAPI Dev Error: ${error.message}`);
          return;
        }
        if (stderr) {
          console.error(`BAPI Dev Stderr: ${stderr}`);
          return;
        }

        console.log(`BAPI Dev Stdout: ${stdout}`);
      });
    }
  }

  if(currentEnv === 'production'){
    if(api === fapi){
      exec(`npx swagger-cli bundle -r ./api/fapi/openapi/versions/2021-02-05.yml -o ./tmp/fapi.env.json && npx @scalar/cli share --token=${fapiToken} ./api/fapi/openapi/versions/2021-02-05.yml`, (error, stdout, stderr) => {
        if (error) {
          console.error(`FAPI Dev Error: ${error.message}`);
          return;
        }
        if (stderr) {
          console.error(`FAPI Dev Stderr: ${stderr}`);
          return;
        }

        console.log(`FAPI Dev Stdout: ${stdout}`);
      });
    }
    if(api === bapi){
      exec(`npx swagger-cli bundle -r ./api/bapi/openapi/versions/2021-02-05.yml -o ./tmp/bapi.env.json && npx @scalar/cli share --token=${bapiToken} ./api/bapi/openapi/versions/2021-02-05.yml`, (error, stdout, stderr) => {
        if (error) {
          console.error(`BAPI Dev Error: ${error.message}`);
          return;
        }
        if (stderr) {
          console.error(`BAPI Dev Stderr: ${stderr}`);
          return;
        }

        console.log(`BAPI Dev Stdout: ${stdout}`);
      });
    }
  }
});
