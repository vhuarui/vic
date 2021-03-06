# Copyright 2016-2017 VMware, Inc. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#	http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License

*** Settings ***
Documentation  Test 6-13 - Verify vic-machine create with TLS
Resource  ../../resources/Util.robot
Test Teardown  Run Keyword If Test Failed  Cleanup VIC Appliance On Test Server

*** Test Cases ***
Create VCH - defaults with --no-tls
    Set Test Environment Variables
    Run Keyword And Ignore Error  Cleanup Dangling VMs On Test Server
    Run Keyword And Ignore Error  Cleanup Datastore On Test Server

    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} --no-tls
    Should Contain  ${output}  Installer completed successfully
    Get Docker Params  ${output}  ${true}
    Log To Console  Installer completed successfully: %{VCH-NAME}


    Run Regression Tests
    Cleanup VIC Appliance On Test Server



Create VCH - force accept target thumbprint
    Set Test Environment Variables
    Run Keyword And Ignore Error  Cleanup Dangling VMs On Test Server
    Run Keyword And Ignore Error  Cleanup Datastore On Test Server

    # Test that --force without --thumbprint accepts the --target thumbprint
    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --force --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} ${vicmachinetls}
    Should Contain  ${output}  Installer completed successfully
    Get Docker Params  ${output}  ${true}
    Log To Console  Installer completed successfully: %{VCH-NAME}

    Run Regression Tests
    Cleanup VIC Appliance On Test Server



Create VCH - Specified keys
    Pass execution  Test not implemented until vic-machine can poll status correctly



Create VCH - Server certificate with multiple blocks
    Set Test Environment Variables
    Run Keyword And Ignore Error  Cleanup Dangling VMs On Test Server
    Run Keyword And Ignore Error  Cleanup Datastore On Test Server

    # Install first to generate certificates
    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} ${vicmachinetls}
    Should Contain  ${output}  Installer completed successfully
    Get Docker Params  ${output}  ${true}
    Log To Console  Installer completed successfully: %{VCH-NAME}

    # Remove the installed VCH
    ${ret}=  Run  bin/vic-machine-linux delete --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --name=%{VCH-NAME} --force
    Should Contain  ${ret}  Completed successfully

    # Update server-cert.pem with a junk block in the beginning
    Run  echo "-----BEGIN RSA PRIVATE KEY-----\nJUNK\n-----END RSA PRIVATE KEY-----" | cat - ./%{VCH-NAME}/server-cert.pem > /tmp/%{VCH-NAME}-server-cert.pem && mv /tmp/%{VCH-NAME}-server-cert.pem ./%{VCH-NAME}/server-cert.pem

    # Install VCH
    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} --no-tlsverify
    Should Contain  ${output}  Failed to load x509 leaf
    Should Contain  ${output}  Loaded server certificate
    Should Contain  ${output}  Installer completed successfully

    Cleanup VIC Appliance On Test Server



Create VCH - Invalid keys
    ${domain}=  Get Environment Variable  DOMAIN  ''
    Run Keyword If  '${domain}' == ''  Pass Execution  Skipping test - domain not set, won't generate keys

    Set Test Environment Variables
    Run Keyword And Ignore Error  Cleanup Dangling VMs On Test Server
    Run Keyword And Ignore Error  Cleanup Datastore On Test Server

    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} ${vicmachinetls}

    # Invalid server key
    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} ${vicmachinetls} --tls-ca="./%{VCH-NAME}/ca.pem" --cert="./%{VCH-NAME}/server-cert.pem" --key="./%{VCH-NAME}/ca.pem"
    Should Contain  ${output}  found a certificate rather than a key in the PEM for the private key

    # Invalid server cert
    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} ${vicmachinetls} --tls-ca="./%{VCH-NAME}/ca.pem" --cert="./%{VCH-NAME}/server-key.pem" --key="./%{VCH-NAME}/server-key.pem"
    Should Contain  ${output}  did find a private key; PEM inputs may have been switched

    # Invalid CA
    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} ${vicmachinetls} --tls-ca="./%{VCH-NAME}/key.pem" --cert="./%{VCH-NAME}/server-cert.pem" --key="./%{VCH-NAME}/server-key.pem"
    Should Contain  ${output}  Unable to load certificate authority data

    Cleanup VIC Appliance On Test Server



Create VCH - Reuse keys
    ${domain}=  Get Environment Variable  DOMAIN  ''
    Run Keyword If  '${domain}' == ''  Pass Execution  Skipping test - domain not set, won't generate keys

    Set Test Environment Variables

    # use one install to generate certificates
    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} ${vicmachinetls}
    Should Contain  ${output}  Installer completed successfully
    Get Docker Params  ${output}  ${true}
    Log To Console  Installer completed successfully: %{VCH-NAME}

    # remove the initial deployment
    ${ret}=  Run  bin/vic-machine-linux delete --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --name=%{VCH-NAME} --force
    Should Contain  ${ret}  Completed successfully

    # deploy using the same name - should reuse certificates
    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} ${vicmachinetls}
    Should Contain  ${output}  Installer completed successfully

    Should Contain  ${output}  Loaded server certificate
    Should Contain  ${output}  Loaded CA with default name from certificate path
    Should Contain  ${output}  Loaded client certificate with default name from certificate path

    Cleanup VIC Appliance On Test Server



Create VCH - Server cert with untrusted CA
    ${domain}=  Get Environment Variable  DOMAIN  ''
    Run Keyword If  '${domain}' == ''  Pass Execution  Skipping test - domain not set, won't generate keys

    Set Test Environment Variables
    Run Keyword And Ignore Error  Cleanup Dangling VMs On Test Server
    Run Keyword And Ignore Error  Cleanup Datastore On Test Server

    # Generate CA and wildcard cert for *.<DOMAIN>
    Run  git clone https://github.com/andrewtchin/ca-test.git
    ${output}=  Run  cd ca-test; ./ca-test.sh -s "*.${domain}"
    Log  ${output}
    Run  cp /root/ca/cert-bundle.tgz .; tar xvf cert-bundle.tgz

    # Run vic-machine install, supply server cert and key
    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --key "bundle/*.${domain}.key.pem" --cert "bundle/*.${domain}.cert.pem" --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} ${vicmachinetls} --debug 1
    Log  ${output}
    Should Contain  ${output}  Loaded server certificate bundle
    Should Contain  ${output}  Unable to locate existing CA in cert path
    Should Contain  ${output}  Failed to find a viable address for Docker API from certificates
    Should Contain  ${output}  Server certificate hostname doesn't match
    Should Contain  ${output}  Installer completed successfully

    # Verify that the supplied certificate is presented on web interface
    Get Docker Params  ${output}  ${true}
    ${output}=  Run  openssl s_client -showcerts -connect %{VCH-IP}:2378
    Log  ${output}
    Should Contain  ${output}  issuer=/C=US/ST=California/L=Los Angeles/O=Stark Enterprises/OU=Stark Enterprises Certificate Authority/CN=Stark Enterprises Global CA

    Run  rm -rf bundle
    Run  rm -f cert-bundle.tgz
    Run  rm -rf /root/ca
    Run  rm -rf ca-test
    Run Keyword And Ignore Error  Cleanup VIC Appliance On Test Server



Create VCH - Server cert with trusted CA
    ${domain}=  Get Environment Variable  DOMAIN  ''
    Run Keyword If  '${domain}' == ''  Pass Execution  Skipping test - domain not set, won't generate keys

    Set Test Environment Variables
    Run Keyword And Ignore Error  Cleanup Dangling VMs On Test Server
    Run Keyword And Ignore Error  Cleanup Datastore On Test Server

    # Generate CA and wildcard cert for *.<DOMAIN>, install CA into root store
    Run  git clone https://github.com/andrewtchin/ca-test.git
    ${output}=  Run  cd ca-test; ./ca-test.sh -s "*.${domain}"; ./ubuntu-install-ca.sh
    Log  ${output}
    Run  cp /root/ca/cert-bundle.tgz .; tar xvf cert-bundle.tgz
    ${output}=  Run   ls -al /usr/local/share/ca-certificates/
    Log  ${output}

    # Run vic-machine install, supply server cert and key
    ${output}=  Run  bin/vic-machine-linux create --name=%{VCH-NAME} --target="%{TEST_USERNAME}:%{TEST_PASSWORD}@%{TEST_URL}" --thumbprint=%{TEST_THUMBPRINT} --key "bundle/*.${domain}.key.pem" --cert "bundle/*.${domain}.cert.pem" --image-store=%{TEST_DATASTORE} --bridge-network=%{BRIDGE_NETWORK} --public-network=%{PUBLIC_NETWORK} ${vicmachinetls} --debug 1
    Log  ${output}
    Should Contain  ${output}  Loaded server certificate bundle
    Should Contain  ${output}  Unable to locate existing CA in cert path
    Should Contain  ${output}  for use against host certificate
    Should Contain  ${output}  Installer completed successfully


    # Verify that the supplied certificate is presented on web interface
    Get Docker Params  ${output}  ${true}
    ${output}=  Run  openssl s_client -showcerts -connect %{VCH-IP}:2378
    Log  ${output}
    Should Contain  ${output}  issuer=/C=US/ST=California/L=Los Angeles/O=Stark Enterprises/OU=Stark Enterprises Certificate Authority/CN=Stark Enterprises Global CA

    ${output}=  Run  ./ca-test/ubuntu-remove-ca.sh
    Log  ${output}

    Run  rm -rf bundle
    Run  rm -f cert-bundle.tgz
    Run  rm -rf /root/ca
    Run  rm -rf ca-test
    Run Keyword And Ignore Error  Cleanup VIC Appliance On Test Server



Create VCH - Server cert with intermediate CA
    ${domain}=  Get Environment Variable  DOMAIN  ''
    Run Keyword If  '${domain}' == ''  Pass Execution  Skipping test - domain not set, won't generate keys
    Pass execution  Test not implemented
