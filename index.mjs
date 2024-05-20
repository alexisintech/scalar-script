// run the scalar/cli bundle with an output of tmp/fapi.env.json
// after we've dummped both fapi and bapi into tmp files
// we run the scalar/cli share with the appropriate token

// over on clerk/clerk we have 2 templates setup that are configured
// to consume the url of the appropriate scalar/cli share URLs that get returned

const fapi =  {
  development: '1234',
  production: '5678'
}

const bapi = {
  development: '4321',
  production: '8765'
}

const APIS = [fapi, bapi]

const currentEnv = ENVS[process.env.NODE_ENV] || 'development';
const fapiToken = api.fapi[currentEnv];
const bapiToken = api.bapi[currentEnv];

const { exec } = require('child_process');

exec('npx @scalar/cli', (error, stdout, stderr) => {
  if (error) {
    console.error(`Error: ${error.message}`);
    return;
  }
  if (stderr) {
    console.error(`Stderr: ${stderr}`);
    return;
  }

  APIS.forEach(api => {
    if(currentEnv === 'development'){
      scalar bundle ./api/fapi/openapi/versions/2021-02-05.yml --output ./api/fapi/tmp/fapi.env.json
      scalar share --token=fapiToken ./api/fapi/tmp/fapi.env.json

      scalar bundle ./api/bapi/openapi/versions/2021-02-05.yml --output ./api/bapi/tmp/bapi.env.json
      scalar share --token=bapiToken ./api/bapi/tmp/bapi.env.json
    }
    if(currentEnv === 'production'){
      scalar bundle ./api/fapi/openapi/versions/2021-02-05.yml --output ./api/fapi/tmp/fapi.env.json
      scalar share --token=fapiToken ./api/fapi/tmp/fapi.env.json

      scalar bundle ./api/bapi/openapi/versions/2021-02-05.yml --output ./api/bapi/tmp/bapi.env.json
      scalar share --token=bapiToken ./api/bapi/tmp/bapi.env.json
    }
  });

  console.log(`Stdout: ${stdout}`);
});
