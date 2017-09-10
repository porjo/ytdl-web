
var ws = new WebSocket("ws://localhost:3000/websocket");

$(function(){

	$("#go-button").click(function() {
		if (ws.readyState === 1) {
			$("#output").show();
			var url = $("#url").val();
			var val = {Key: 'url', Value: url};
			ws.send(JSON.stringify(val));
			$("#status").append(url + "\n");
		} else {
			$("#status").append("socket not ready\n")
		}
	});

	ws.onopen = function() {
	};

	ws.onmessage = function (e)	{
		var msg = JSON.parse(e.data);
		console.log(msg);
		if( 'Key' in msg ) {
			switch (msg.Key) {
				case 'error':
					$("#status").append("Error: " + msg.Value + "\n");
					break;
				case 'progress':
					var pct = parseFloat(msg.Value.Pct);
					$("#progress-bar > span").css("width", pct + "%")
						.text(pct + "%");
					$("#eta").text( msg.Value.ETA );
					break;
				case 'info':
					$("#title").text( msg.Value.Title );
					$("#filesize").text( msg.Value.Filesize );
					break;
				case 'link':
					$("#link").attr('href', msg.Value.DownloadURL)
						.css('display', 'inline-block');
					break;
			}
		}
	};

	ws.onclose = function()	{
			$("#status").append("Connection closed\n");
	};

});
