# GLORP

Glorp is an HTTP intercept proxy, allowing the inspection and replaying of HTTP requests. The layout and flow was designed to function similar to Portswigger's Burp Proxy and Repeater tabs. The proxy functionality is done using [Google's Martian](https://github.com/google/martian), UI is done with [TView](https://github.com/rivo/tview).

The idea is to provide a CLI based tool for when you wanna-look-at-this-thing-real-quick and not fire up yet another full-fat container/vm/whatever with Burp and so forth.

![page switching](./gif/glorp.gif)

## Install

Install can be done with `git clone` and `go build/install`, or by using one of the binaries available on the releases page.

Alternatively, to run under docker, clone this repository and:

```
docker build -tglorp .
docker run -p 8080:8080 --rm -it glorp
```

## Command Line Flags

```
Usage of ./glorp:
  -addr string
    	The bind address, default 0.0.0.0
  -cert string
    	Path to a CA Certificate
  -help
    	Show help
  -key string
    	Path to the CA cert's private key
  -port uint
    	Listen port for the proxy, default 8080
  -v int
    	log level
```

### Using a custom CA

You'll probably want to specify a CA file, so you can load this into your browser/mobile device/operating system/whatever. The easiest way to spin up your own CA for use in Glorp is as follows:

```
doi@buzdovan:~/go/src/glorp$ openssl genrsa -out ca.key 2048
Generating RSA private key, 2048 bit long modulus (2 primes)
.....................+++++
...+++++
e is 65537 (0x010001)
doi@buzdovan:~/go/src/glorp$ openssl req -x509 -new -nodes -key ca.key -sha256 -days 1825 -out ca.crt
You are about to be asked to enter information that will be incorporated
into your certificate request.
What you are about to enter is what is called a Distinguished Name or a DN.
There are quite a few fields but you can leave some blank
For some fields there will be a default value,
If you enter '.', the field will be left blank.
-----
Country Name (2 letter code) [AU]:
State or Province Name (full name) [Some-State]:
Locality Name (eg, city) []:
Organization Name (eg, company) [Internet Widgits Pty Ltd]:
Organizational Unit Name (eg, section) []:
Common Name (e.g. server FQDN or YOUR name) []:
Email Address []:

```

You can happily enter-enter-enter your way through the dialog above, then launch glorp:

```
doi@buzdovan:~/go/src/glorp$ ./glorp -cert ca.crt -key ca.key
```

## UI Usage

Key | View | Details
--|--|--
tab | All | Go to next element (window, button, etc) in the page
shift+tab | All | Go to previous element in the page
ctrl-c | All | Exit Glorp
ctrl-n | All | Go the next page
ctrl-p | All | Go to the previous page
ctrl-r | Proxy/Replay | Send item to the replayer
ctrl-s | Proxy/Replay - highlighted request/response | Save item to file
g      | Proxy | Go to first entry in the proxy table
G      | Proxy | Go to last entry in the proxy table
/      | Proxy | Enter a search-filter regex to filter proxy entries by URL
ctrl-e | Proxy - highlighted request/response | Open the request/response data in `view`
ctrl-b | Replay | Create a new blank replay item - useful for assembling requests from scratch
ctrl-e | Replay - highlighted request/response | Edit request in `vi`, responses will open with `view`
ctrl-x | Replay | Rename replay item
ctrl-g | Replay | Send the request


Ctrl-N and Ctrl-P cycle between the different pages, Tab/Shift+tab is used to cycle between each item within a page.

### Proxy Page

The proxy page shows incoming requests. If you select the last item (bottom item), then the view will follow new requests.

## Replay Page

In the proxy page, hit `ctrl-r` on an entry and it will be sent to the replay page, where you can modify the request and re-issue it. If you hit `ctrl-r` in the Replay page, it'll duplicated the current item.

### Editing

Highlight the request text box and hit `ctrl-e`. This will open the request in VI and let you edit it. 

Pro-tip for content length: If you highlight your modified request body in visual mode (`v`) and then hit `g`->`ctrl+g` it will show you how many bytes are selected, and you can update the content-length header accordingly.

## Log Page

This is the general log info page and takes no user input. Glorp is set up such that any call to `log.Println` or similar will end up in this view. 

## Save/Load Page

This one should hopefully be self explanatory. Lets you save and load all the proxy entries and replay entries. Writes out to a JSON file or reads in a JSON file. WARNING: Loading will delete all existing proxy and replay entries, rather than append to them.

## Transparent Proxying

Glorp does not support transparent proxying, but squid does :D Rather than build this logic into Glorp, I figure run a squid proxy and forward it through. The squid config should look like:

```
acl all src 0.0.0.0/0
http_access allow all

http_port 3128 
http_port 3080 intercept
https_port 3443 ssl-bump intercept \
  cert=<PATH TO KEY AND CERT IN ONE PEM> \
  generate-host-certificates=on dynamic_cert_mem_cache_size=4MB

sslcrtd_program /usr/local/squid/libexec/security_file_certgen -s /var/lib/ssl_db -M 4MB
acl step1 at_step SslBump1
ssl_bump peek step1
ssl_bump bump all

# forward to glorp
cache_peer 127.0.0.1 parent 8080 0 no-query default
never_direct allow all
sslproxy_cert_error allow all
sslproxy_flags DONT_VERIFY_PEER
sslproxy_cert_error allow all
sslproxy_flags DONT_VERIFY_PEER
```

Use iptables to hijack the connection:

```
iptables -t nat -A PREROUTING -i enp1s0 -p tcp --dport 80 -j REDIRECT --to-port 3080
iptables -t nat -A PREROUTING -i enp1s0 -p tcp --dport 443 -j REDIRECT --to-port 3443
iptables -t nat -A POSTROUTING -o enp1s0 -j MASQUERADE
```

Squid can be built with the following dockerfile:

```
# docker run --net=host -it --rm -v $PWD:/etc/squid sq1 /usr/local/squid/sbin/squid -N -f /etc/squid/squid.conf
# Net host saves some docker iptables headaches, should probably document how to do that properly...
FROM debian:latest

WORKDIR /opt/

RUN apt update 
RUN apt upgrade -y
RUN apt install -y automake libtool build-essential libssl-dev git ca-certificates

## clone and build squid
RUN git clone https://github.com/squid-cache/squid && cd squid && autoreconf -i
RUN cd /opt/squid && ./configure --prefix=/usr/local/squid --with-openssl --enable-ssl-crtd
RUN cd /opt/squid && make -j4 && make install

# sort the log file dir perms and create the ssl junk
RUN chown nobody /usr/local/squid/var/logs/
RUN /usr/local/squid/libexec/security_file_certgen -c -s /var/lib/ssl_db -M 4MB
```
